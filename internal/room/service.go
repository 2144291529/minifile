package room

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"gossh/internal/config"
	"gossh/internal/domain"
	"gossh/internal/meta"
	"gossh/internal/storage"
)

type Service struct {
	cfg     config.Config
	meta    *meta.Store
	storage storage.Backend
	logger  *slog.Logger
}

var roomIDPattern = regexp.MustCompile(`^[A-Z0-9_-]{4,32}$`)

func NewService(cfg config.Config, metaStore *meta.Store, backend storage.Backend, logger *slog.Logger) *Service {
	return &Service{
		cfg:     cfg,
		meta:    metaStore,
		storage: backend,
		logger:  logger.With("component", "room-service"),
	}
}

func (s *Service) CreateRoom(ctx context.Context, requestedID string) (domain.Room, error) {
	now := time.Now().UTC()
	roomID, err := normalizeRoomID(requestedID)
	if err != nil {
		return domain.Room{}, err
	}
	if roomID == "" {
		roomID = newRoomID()
	}
	room := domain.Room{
		ID:             roomID,
		Status:         domain.RoomStatusReady,
		StorageBackend: s.cfg.StorageBackend,
		ExpiresAt:      now.Add(s.cfg.RoomTTL),
		ChunkSize:      s.cfg.DefaultChunkSize,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.meta.CreateRoom(ctx, room); err != nil {
		return domain.Room{}, err
	}
	_ = s.AppendEvent(ctx, domain.RoomEvent{
		ID:        newEventID(),
		Type:      "room.created",
		Room:      room.ID,
		Message:   "Room created",
		CreatedAt: now,
	})
	return room, nil
}

func (s *Service) GetRoom(ctx context.Context, roomID string) (domain.Room, error) {
	return s.meta.GetRoom(ctx, roomID)
}

func (s *Service) CreateTransfer(ctx context.Context, roomID, sender string) (domain.Transfer, error) {
	now := time.Now().UTC()
	transfer := domain.Transfer{
		ID:        newTransferID(),
		RoomID:    roomID,
		Sender:    sender,
		Status:    domain.TransferStatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.meta.CreateTransfer(ctx, transfer); err != nil {
		return domain.Transfer{}, err
	}
	_ = s.AppendEvent(ctx, domain.RoomEvent{
		ID:         newEventID(),
		Type:       "transfer.created",
		Room:       roomID,
		Actor:      sender,
		TransferID: transfer.ID,
		Status:     transfer.Status,
		Message:    sender + " is preparing a file",
		CreatedAt:  now,
	})
	return transfer, nil
}

func (s *Service) GetTransfer(ctx context.Context, roomID, transferID string) (domain.Transfer, error) {
	return s.meta.GetTransfer(ctx, roomID, transferID)
}

func (s *Service) SaveManifest(ctx context.Context, roomID, transferID string, chunkSize int64, totalChunks int, body io.Reader, size int64) (domain.Transfer, error) {
	transfer, err := s.meta.UpdateTransferManifest(ctx, roomID, transferID, chunkSize, totalChunks, size)
	if err != nil {
		return domain.Transfer{}, err
	}
	if err := s.storage.Put(ctx, manifestKey(roomID, transferID), body, size, "application/octet-stream"); err != nil {
		return domain.Transfer{}, err
	}
	_ = s.AppendEvent(ctx, domain.RoomEvent{
		ID:         newEventID(),
		Type:       "transfer.started",
		Room:       roomID,
		Actor:      transfer.Sender,
		TransferID: transferID,
		Status:     transfer.Status,
		Message:    transfer.Sender + " started uploading",
		CreatedAt:  time.Now().UTC(),
	})
	return transfer, nil
}

func (s *Service) GetManifest(ctx context.Context, roomID, transferID string) (io.ReadCloser, storage.ObjectInfo, error) {
	return s.storage.Get(ctx, manifestKey(roomID, transferID))
}

func (s *Service) PutChunk(ctx context.Context, roomID, transferID string, index int, checksum string, body io.Reader, size int64) (domain.Transfer, error) {
	if err := s.storage.Put(ctx, chunkKey(roomID, transferID, index), body, size, "application/octet-stream"); err != nil {
		return domain.Transfer{}, err
	}

	transfer, err := s.meta.UpsertChunk(ctx, domain.Chunk{
		RoomID:      roomID,
		TransferID:  transferID,
		Index:       index,
		Size:        size,
		Checksum:    checksum,
		ContentType: "application/octet-stream",
		CreatedAt:   time.Now().UTC(),
	})
	if err != nil {
		return domain.Transfer{}, err
	}

	if transfer.Status == domain.TransferStatusReady {
		_ = s.AppendEvent(ctx, domain.RoomEvent{
			ID:         newEventID(),
			Type:       "transfer.ready",
			Room:       roomID,
			Actor:      transfer.Sender,
			TransferID: transferID,
			Status:     transfer.Status,
			Message:    transfer.Sender + " sent a file",
			CreatedAt:  time.Now().UTC(),
			Data: map[string]interface{}{
				"uploadedChunks": transfer.UploadedChunks,
				"totalChunks":    transfer.TotalChunks,
				"bytesReceived":  transfer.BytesReceived,
			},
		})
	}
	return transfer, nil
}

func (s *Service) GetChunk(ctx context.Context, roomID, transferID string, index int) (io.ReadCloser, storage.ObjectInfo, error) {
	return s.storage.Get(ctx, chunkKey(roomID, transferID, index))
}

func (s *Service) GetResumeState(ctx context.Context, roomID, transferID string) (domain.ResumeState, error) {
	return s.meta.GetResumeState(ctx, roomID, transferID)
}

func (s *Service) MarkDownloading(ctx context.Context, roomID, transferID, actor string) (domain.Transfer, error) {
	transfer, err := s.meta.GetTransfer(ctx, roomID, transferID)
	if err != nil {
		return domain.Transfer{}, err
	}
	_ = s.AppendEvent(ctx, domain.RoomEvent{
		ID:         newEventID(),
		Type:       "transfer.downloading",
		Room:       roomID,
		Actor:      actor,
		TransferID: transferID,
		Status:     transfer.Status,
		Message:    actor + " started downloading",
		CreatedAt:  time.Now().UTC(),
	})
	return transfer, nil
}

func (s *Service) DeleteTransfer(ctx context.Context, roomID, transferID, actor string) error {
	transfer, err := s.meta.GetTransfer(ctx, roomID, transferID)
	if err != nil {
		return err
	}
	if err := s.storage.DeletePrefix(ctx, fmt.Sprintf("%s/transfers/%s", roomPrefix(roomID), transferID)); err != nil {
		return err
	}
	if err := s.meta.DeleteTransfer(ctx, roomID, transferID); err != nil {
		return err
	}
	if actor == "" {
		actor = "Anonymous"
	}
	return s.AppendEvent(ctx, domain.RoomEvent{
		ID:         newEventID(),
		Type:       "transfer.deleted",
		Room:       roomID,
		Actor:      actor,
		TransferID: transferID,
		Message:    actor + " deleted " + transfer.ID,
		CreatedAt:  time.Now().UTC(),
	})
}

func (s *Service) GetRoomSnapshot(ctx context.Context, roomID string, online int) (domain.RoomSnapshot, error) {
	room, err := s.meta.GetRoom(ctx, roomID)
	if err != nil {
		return domain.RoomSnapshot{}, err
	}
	transfers, err := s.meta.ListTransfers(ctx, roomID)
	if err != nil {
		return domain.RoomSnapshot{}, err
	}
	events, err := s.meta.ListEvents(ctx, roomID)
	if err != nil {
		return domain.RoomSnapshot{}, err
	}
	return domain.RoomSnapshot{
		Room:      room,
		Online:    online,
		Transfers: transfers,
		Events:    events,
	}, nil
}

func (s *Service) AppendEvent(ctx context.Context, event domain.RoomEvent) error {
	return s.meta.AppendEvent(ctx, event)
}

func (s *Service) CleanupExpired(ctx context.Context) error {
	ids, err := s.meta.DeleteExpiredRooms(ctx, time.Now().UTC())
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.storage.DeletePrefix(ctx, roomPrefix(id)); err != nil {
			s.logger.Warn("delete expired objects failed", "room", id, "err", err)
		}
	}
	return nil
}

func manifestKey(roomID, transferID string) string {
	return fmt.Sprintf("%s/transfers/%s/manifest.bin", roomPrefix(roomID), transferID)
}

func chunkKey(roomID, transferID string, index int) string {
	return fmt.Sprintf("%s/transfers/%s/chunks/%06d.bin", roomPrefix(roomID), transferID, index)
}

func roomPrefix(roomID string) string {
	return fmt.Sprintf("rooms/%s", roomID)
}

func newRoomID() string {
	var raw [10]byte
	_, _ = rand.Read(raw[:])
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:])
}

func normalizeRoomID(value string) (string, error) {
	value = strings.TrimSpace(strings.ToUpper(value))
	if value == "" {
		return "", nil
	}
	if !roomIDPattern.MatchString(value) {
		return "", errors.New("room id must be 4-32 chars of A-Z, 0-9, _ or -")
	}
	return value, nil
}

func newTransferID() string {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	return "tx_" + hex.EncodeToString(raw[:])
}

func newEventID() string {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	return "evt_" + hex.EncodeToString(raw[:])
}
