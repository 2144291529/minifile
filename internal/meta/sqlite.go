package meta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"gossh/internal/domain"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	path string

	mu        sync.RWMutex
	rooms     map[string]domain.Room
	transfers map[string]map[string]domain.Transfer
	chunks    map[string]map[string]map[int]domain.Chunk
	events    map[string][]domain.RoomEvent
}

type snapshot struct {
	Rooms     map[string]domain.Room                     `json:"rooms"`
	Transfers map[string]map[string]domain.Transfer      `json:"transfers"`
	Chunks    map[string]map[string]map[int]domain.Chunk `json:"chunks"`
	Events    map[string][]domain.RoomEvent              `json:"events"`
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create meta dir: %w", err)
	}

	s := &Store{
		path:      path,
		rooms:     make(map[string]domain.Room),
		transfers: make(map[string]map[string]domain.Transfer),
		chunks:    make(map[string]map[string]map[int]domain.Chunk),
		events:    make(map[string][]domain.RoomEvent),
	}

	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.persist()
}

func (s *Store) CreateRoom(_ context.Context, room domain.Room) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.rooms[room.ID]; exists {
		return fmt.Errorf("room already exists")
	}
	s.rooms[room.ID] = room
	return s.persistLocked()
}

func (s *Store) GetRoom(_ context.Context, roomID string) (domain.Room, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	room, ok := s.rooms[roomID]
	if !ok {
		return domain.Room{}, ErrNotFound
	}
	return room, nil
}

func (s *Store) CreateTransfer(_ context.Context, transfer domain.Transfer) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.rooms[transfer.RoomID]; !ok {
		return ErrNotFound
	}
	if _, ok := s.transfers[transfer.RoomID]; !ok {
		s.transfers[transfer.RoomID] = make(map[string]domain.Transfer)
	}
	s.transfers[transfer.RoomID][transfer.ID] = transfer
	return s.persistLocked()
}

func (s *Store) GetTransfer(_ context.Context, roomID, transferID string) (domain.Transfer, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getTransferLocked(roomID, transferID)
}

func (s *Store) getTransferLocked(roomID, transferID string) (domain.Transfer, error) {
	roomTransfers, ok := s.transfers[roomID]
	if !ok {
		return domain.Transfer{}, ErrNotFound
	}
	transfer, ok := roomTransfers[transferID]
	if !ok {
		return domain.Transfer{}, ErrNotFound
	}
	return transfer, nil
}

func (s *Store) UpdateTransferManifest(_ context.Context, roomID, transferID string, chunkSize int64, totalChunks int, manifestSize int64) (domain.Transfer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok := s.rooms[roomID]
	if !ok {
		return domain.Transfer{}, ErrNotFound
	}
	transfer, err := s.getTransferLocked(roomID, transferID)
	if err != nil {
		return domain.Transfer{}, err
	}

	transfer.Status = domain.TransferStatusUploading
	transfer.ChunkSize = chunkSize
	transfer.TotalChunks = totalChunks
	transfer.ManifestSize = manifestSize
	transfer.UpdatedAt = time.Now().UTC()
	s.transfers[roomID][transferID] = transfer

	room.Status = domain.RoomStatusUploading
	room.ChunkSize = chunkSize
	room.TotalChunks = totalChunks
	room.ManifestSize = manifestSize
	room.UpdatedAt = transfer.UpdatedAt
	s.rooms[roomID] = room

	return transfer, s.persistLocked()
}

func (s *Store) UpsertChunk(_ context.Context, chunk domain.Chunk) (domain.Transfer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	room, ok := s.rooms[chunk.RoomID]
	if !ok {
		return domain.Transfer{}, ErrNotFound
	}
	transfer, err := s.getTransferLocked(chunk.RoomID, chunk.TransferID)
	if err != nil {
		return domain.Transfer{}, err
	}
	if _, ok := s.chunks[chunk.RoomID]; !ok {
		s.chunks[chunk.RoomID] = make(map[string]map[int]domain.Chunk)
	}
	if _, ok := s.chunks[chunk.RoomID][chunk.TransferID]; !ok {
		s.chunks[chunk.RoomID][chunk.TransferID] = make(map[int]domain.Chunk)
	}
	s.chunks[chunk.RoomID][chunk.TransferID][chunk.Index] = chunk

	transfer.UploadedChunks = len(s.chunks[chunk.RoomID][chunk.TransferID])
	var bytesReceived int64
	for _, item := range s.chunks[chunk.RoomID][chunk.TransferID] {
		bytesReceived += item.Size
	}
	transfer.BytesReceived = bytesReceived
	if transfer.TotalChunks > 0 && transfer.UploadedChunks >= transfer.TotalChunks {
		transfer.Status = domain.TransferStatusReady
	} else {
		transfer.Status = domain.TransferStatusUploading
	}
	transfer.UpdatedAt = time.Now().UTC()
	s.transfers[chunk.RoomID][chunk.TransferID] = transfer

	room.UploadedChunks = transfer.UploadedChunks
	room.BytesReceived = transfer.BytesReceived
	if transfer.Status == domain.TransferStatusReady {
		room.Status = domain.RoomStatusUploaded
	} else {
		room.Status = domain.RoomStatusUploading
	}
	room.UpdatedAt = transfer.UpdatedAt
	s.rooms[chunk.RoomID] = room

	return transfer, s.persistLocked()
}

func (s *Store) GetResumeState(_ context.Context, roomID, transferID string) (domain.ResumeState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	room, ok := s.rooms[roomID]
	if !ok {
		return domain.ResumeState{}, ErrNotFound
	}
	transfer, err := s.getTransferLocked(roomID, transferID)
	if err != nil {
		return domain.ResumeState{}, err
	}

	indices := make([]int, 0, len(s.chunks[roomID][transferID]))
	for idx := range s.chunks[roomID][transferID] {
		indices = append(indices, idx)
	}
	sort.Ints(indices)
	return domain.ResumeState{
		RoomID:         room.ID,
		TransferID:     transfer.ID,
		ChunkSize:      transfer.ChunkSize,
		TotalChunks:    transfer.TotalChunks,
		UploadedChunks: indices,
		Completed:      transfer.TotalChunks > 0 && len(indices) >= transfer.TotalChunks,
		ExpiresAt:      room.ExpiresAt,
	}, nil
}

func (s *Store) ListTransfers(_ context.Context, roomID string) ([]domain.Transfer, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	roomTransfers := s.transfers[roomID]
	if roomTransfers == nil {
		return nil, nil
	}

	out := make([]domain.Transfer, 0, len(roomTransfers))
	for _, transfer := range roomTransfers {
		out = append(out, transfer)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (s *Store) AppendEvent(_ context.Context, event domain.RoomEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.events[event.Room] = append(s.events[event.Room], event)
	if len(s.events[event.Room]) > 100 {
		s.events[event.Room] = s.events[event.Room][len(s.events[event.Room])-100:]
	}
	return s.persistLocked()
}

func (s *Store) ListEvents(_ context.Context, roomID string) ([]domain.RoomEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	events := s.events[roomID]
	if len(events) == 0 {
		return nil, nil
	}
	out := make([]domain.RoomEvent, len(events))
	copy(out, events)
	return out, nil
}

func (s *Store) MarkRoomStatus(_ context.Context, roomID string, status domain.RoomStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	room, ok := s.rooms[roomID]
	if !ok {
		return ErrNotFound
	}
	room.Status = status
	room.UpdatedAt = time.Now().UTC()
	s.rooms[roomID] = room
	return s.persistLocked()
}

func (s *Store) UpdateTransferStatus(_ context.Context, roomID, transferID string, status domain.TransferStatus) (domain.Transfer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	transfer, err := s.getTransferLocked(roomID, transferID)
	if err != nil {
		return domain.Transfer{}, err
	}
	transfer.Status = status
	transfer.UpdatedAt = time.Now().UTC()
	s.transfers[roomID][transferID] = transfer
	return transfer, s.persistLocked()
}

func (s *Store) DeleteTransfer(_ context.Context, roomID, transferID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	roomTransfers, ok := s.transfers[roomID]
	if !ok {
		return ErrNotFound
	}
	transfer, ok := roomTransfers[transferID]
	if !ok {
		return ErrNotFound
	}
	delete(roomTransfers, transferID)
	if len(roomTransfers) == 0 {
		delete(s.transfers, roomID)
	}

	if roomChunks, ok := s.chunks[roomID]; ok {
		delete(roomChunks, transferID)
		if len(roomChunks) == 0 {
			delete(s.chunks, roomID)
		}
	}

	if room, ok := s.rooms[roomID]; ok {
		room.Status = domain.RoomStatusReady
		room.TotalChunks = 0
		room.UploadedChunks = 0
		room.BytesReceived = 0
		room.ManifestSize = 0
		room.UpdatedAt = time.Now().UTC()
		if remaining, ok := s.transfers[roomID]; ok && len(remaining) > 0 {
			latest := transfer
			for _, item := range remaining {
				if item.UpdatedAt.After(latest.UpdatedAt) {
					latest = item
				}
			}
			room.ChunkSize = latest.ChunkSize
			room.TotalChunks = latest.TotalChunks
			room.UploadedChunks = latest.UploadedChunks
			room.BytesReceived = latest.BytesReceived
			room.ManifestSize = latest.ManifestSize
			switch latest.Status {
			case domain.TransferStatusReady:
				room.Status = domain.RoomStatusUploaded
			case domain.TransferStatusDownloading:
				room.Status = domain.RoomStatusDownloading
			case domain.TransferStatusUploading:
				room.Status = domain.RoomStatusUploading
			default:
				room.Status = domain.RoomStatusReady
			}
		}
		s.rooms[roomID] = room
	}

	return s.persistLocked()
}

func (s *Store) DeleteExpiredRooms(_ context.Context, before time.Time) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var ids []string
	for id, room := range s.rooms {
		if !room.ExpiresAt.After(before) {
			ids = append(ids, id)
			delete(s.rooms, id)
			delete(s.transfers, id)
			delete(s.chunks, id)
			delete(s.events, id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	sort.Strings(ids)
	return ids, s.persistLocked()
}

func (s *Store) load() error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read meta store: %w", err)
	}

	var snap snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		backup := s.path + ".bak-" + time.Now().UTC().Format("20060102-150405")
		if writeErr := os.WriteFile(backup, raw, 0o644); writeErr != nil {
			return fmt.Errorf("decode meta store: %w; backup failed: %v", err, writeErr)
		}
		return nil
	}
	if snap.Rooms != nil {
		s.rooms = snap.Rooms
	}
	if snap.Transfers != nil {
		s.transfers = snap.Transfers
	}
	if snap.Chunks != nil {
		s.chunks = snap.Chunks
	}
	if snap.Events != nil {
		s.events = snap.Events
	}
	return nil
}

func (s *Store) persist() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.persistSnapshot(snapshot{
		Rooms:     s.rooms,
		Transfers: s.transfers,
		Chunks:    s.chunks,
		Events:    s.events,
	})
}

func (s *Store) persistLocked() error {
	return s.persistSnapshot(snapshot{
		Rooms:     s.rooms,
		Transfers: s.transfers,
		Chunks:    s.chunks,
		Events:    s.events,
	})
}

func (s *Store) persistSnapshot(snap snapshot) error {
	raw, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("encode meta store: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return fmt.Errorf("write meta store: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("commit meta store: %w", err)
	}
	return nil
}
