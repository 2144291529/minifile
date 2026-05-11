package app

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gossh/internal/config"
	"gossh/internal/domain"
	"gossh/internal/meta"
	"gossh/internal/room"
	"gossh/internal/storage"
	"gossh/internal/web"
	"gossh/internal/webrtc"
	"gossh/internal/ws"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

type Server struct {
	cfg         config.Config
	logger      *slog.Logger
	metaStore   *meta.Store
	roomService *room.Service
	wsHub       *ws.Hub
	plainServer *http.Server
	httpServer  *http.Server
	http3Server *http3.Server
}

func New(cfg config.Config, logger *slog.Logger) (*Server, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.DatabasePath), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.CertFile), 0o755); err != nil {
		return nil, fmt.Errorf("create cert dir: %w", err)
	}
	if err := ensureTLSMaterial(cfg.CertFile, cfg.KeyFile); err != nil {
		return nil, fmt.Errorf("ensure tls material: %w", err)
	}

	metaStore, err := meta.Open(cfg.DatabasePath)
	if err != nil {
		return nil, err
	}

	var backend storage.Backend
	switch strings.ToLower(cfg.StorageBackend) {
	case "s3":
		backend, err = storage.NewS3(cfg.S3)
	default:
		backend, err = storage.NewLocal(cfg.LocalStorageDir)
	}
	if err != nil {
		_ = metaStore.Close()
		return nil, err
	}

	roomService := room.NewService(cfg, metaStore, backend, logger)
	hub := ws.NewHub(logger, cfg.WebsocketBufferSize)

	s := &Server{
		cfg:         cfg,
		logger:      logger,
		metaStore:   metaStore,
		roomService: roomService,
		wsHub:       hub,
	}
	s.plainServer, s.httpServer, s.http3Server = s.buildServers()
	return s, nil
}

func (s *Server) Run(ctx context.Context) error {
	defer s.metaStore.Close()
	go s.cleanupLoop(ctx)

	errCh := make(chan error, 3)

	go func() {
		s.logger.Info("http server listening", "addr", s.cfg.PlainHTTPAddr)
		if err := s.plainServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	go func() {
		s.logger.Info("https server listening", "addr", s.cfg.HTTPAddr)
		if err := s.httpServer.ListenAndServeTLS(s.cfg.CertFile, s.cfg.KeyFile); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	go func() {
		s.logger.Info("http3 server listening", "addr", s.cfg.HTTPAddr)
		if err := s.http3Server.ListenAndServeTLS(s.cfg.CertFile, s.cfg.KeyFile); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.http3Server.Close()
		_ = s.httpServer.Shutdown(shutdownCtx)
		return s.plainServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func (s *Server) buildServers() (*http.Server, *http.Server, *http3.Server) {
	mux := http.NewServeMux()
	mux.Handle("/", s.staticHandler())
	mux.HandleFunc("/api/v1/rooms", s.handleCreateRoom)
	mux.HandleFunc("/api/v1/rooms/", s.handleRoomRoutes)
	mux.HandleFunc("/ws/rooms/", s.handleRoomWS)

	http3Announce := &http3.Server{Addr: s.cfg.HTTPAddr}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor < 3 {
			_ = http3Announce.SetQUICHeaders(w.Header())
		}
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Embedder-Policy", "require-corp")
		mux.ServeHTTP(w, r)
	})

	plainServer := &http.Server{
		Addr:    s.cfg.PlainHTTPAddr,
		Handler: s.redirectInsecureRemoteTraffic(mux),
	}
	httpServer := &http.Server{
		Addr:    s.cfg.HTTPAddr,
		Handler: handler,
	}
	http3Server := &http3.Server{
		Addr:    s.cfg.HTTPAddr,
		Handler: handler,
		TLSConfig: http3.ConfigureTLSConfig(&tls.Config{
			MinVersion: tls.VersionTLS13,
		}),
		QUICConfig: &quic.Config{KeepAlivePeriod: 15 * time.Second},
	}
	return plainServer, httpServer, http3Server
}

func (s *Server) redirectInsecureRemoteTraffic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isLoopbackHost(r.Host) {
			next.ServeHTTP(w, r)
			return
		}

		targetHost := hostWithoutPort(r.Host)
		if targetHost == "" {
			targetHost = r.Host
		}

		redirectURL := "https://" + net.JoinHostPort(targetHost, portOrDefault(s.cfg.HTTPAddr, "8443")) + r.URL.RequestURI()
		http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
	})
}

func (s *Server) staticHandler() http.Handler {
	sub, err := fsSub(web.Static, "static")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			http.ServeFileFS(w, r, sub, "index.html")
		case strings.HasPrefix(r.URL.Path, "/static/"):
			http.StripPrefix("/static/", fileServer).ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

func (s *Server) handleCreateRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	roomInfo, err := s.roomService.CreateRoom(r.Context(), r.URL.Query().Get("roomId"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"room": roomInfo,
		"webrtc": webrtc.Capability{
			Mode:             modeFromWebRTC(s.cfg.WebRTC.Enabled),
			STUNServers:      s.cfg.WebRTC.STUNServers,
			TURNServers:      s.cfg.WebRTC.TURNServers,
			RelayRequired:    !s.cfg.WebRTC.Enabled,
			FallbackStrategy: s.cfg.WebRTC.RelayFallback,
		},
	})
}

func (s *Server) handleRoomRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/rooms/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	roomID := parts[0]

	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		s.handleRoomSnapshot(w, r, roomID)
	case len(parts) == 2 && parts[1] == "transfers" && r.Method == http.MethodPost:
		s.handleCreateTransfer(w, r, roomID)
	case len(parts) == 3 && parts[1] == "transfers" && r.Method == http.MethodGet:
		s.handleGetTransfer(w, r, roomID, parts[2])
	case len(parts) == 3 && parts[1] == "transfers" && r.Method == http.MethodDelete:
		s.handleDeleteTransfer(w, r, roomID, parts[2])
	case len(parts) == 4 && parts[1] == "transfers" && parts[3] == "resume" && r.Method == http.MethodGet:
		s.handleResume(w, r, roomID, parts[2])
	case len(parts) == 4 && parts[1] == "transfers" && parts[3] == "manifest" && r.Method == http.MethodPut:
		s.handlePutManifest(w, r, roomID, parts[2])
	case len(parts) == 4 && parts[1] == "transfers" && parts[3] == "manifest" && r.Method == http.MethodGet:
		s.handleGetManifest(w, r, roomID, parts[2])
	case len(parts) == 5 && parts[1] == "transfers" && parts[3] == "chunks" && r.Method == http.MethodPut:
		s.handlePutChunk(w, r, roomID, parts[2], parts[4])
	case len(parts) == 5 && parts[1] == "transfers" && parts[3] == "chunks" && r.Method == http.MethodGet:
		s.handleGetChunk(w, r, roomID, parts[2], parts[4])
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleRoomSnapshot(w http.ResponseWriter, r *http.Request, roomID string) {
	snapshot, err := s.roomService.GetRoomSnapshot(r.Context(), roomID, s.wsHub.OnlineCount(roomID))
	if err != nil {
		s.writeMetaErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handleCreateTransfer(w http.ResponseWriter, r *http.Request, roomID string) {
	sender := strings.TrimSpace(r.URL.Query().Get("sender"))
	if sender == "" {
		sender = "Anonymous"
	}
	transfer, err := s.roomService.CreateTransfer(r.Context(), roomID, sender)
	if err != nil {
		s.writeMetaErr(w, err)
		return
	}
	s.broadcastSnapshot(r.Context(), roomID)
	s.writeJSON(w, http.StatusCreated, transfer)
}

func (s *Server) handleDeleteTransfer(w http.ResponseWriter, r *http.Request, roomID, transferID string) {
	actor := strings.TrimSpace(r.URL.Query().Get("actor"))
	if err := s.roomService.DeleteTransfer(r.Context(), roomID, transferID, actor); err != nil {
		s.writeStorageErr(w, err)
		return
	}
	s.broadcastSnapshot(r.Context(), roomID)
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "transfer deleted"})
}

func (s *Server) handleGetTransfer(w http.ResponseWriter, r *http.Request, roomID, transferID string) {
	transfer, err := s.roomService.GetTransfer(r.Context(), roomID, transferID)
	if err != nil {
		s.writeMetaErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, transfer)
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request, roomID, transferID string) {
	state, err := s.roomService.GetResumeState(r.Context(), roomID, transferID)
	if err != nil {
		s.writeMetaErr(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, state)
}

func (s *Server) handlePutManifest(w http.ResponseWriter, r *http.Request, roomID, transferID string) {
	chunkSize, _ := strconv.ParseInt(r.URL.Query().Get("chunkSize"), 10, 64)
	totalChunks, _ := strconv.Atoi(r.URL.Query().Get("totalChunks"))
	if chunkSize <= 0 {
		chunkSize = s.cfg.DefaultChunkSize
	}
	if totalChunks < 0 {
		totalChunks = 0
	}
	body := http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadSize)
	transfer, err := s.roomService.SaveManifest(r.Context(), roomID, transferID, chunkSize, totalChunks, body, r.ContentLength)
	if err != nil {
		s.writeStorageErr(w, err)
		return
	}
	s.broadcastEvent(domain.RoomEvent{
		Type:       "transfer.updated",
		Room:       roomID,
		TransferID: transferID,
		Status:     transfer.Status,
		CreatedAt:  time.Now().UTC(),
		Data:       transfer,
	})
	s.broadcastSnapshot(r.Context(), roomID)
	s.writeJSON(w, http.StatusAccepted, map[string]string{"status": "manifest stored"})
}

func (s *Server) handleGetManifest(w http.ResponseWriter, r *http.Request, roomID, transferID string) {
	reader, info, err := s.roomService.GetManifest(r.Context(), roomID, transferID)
	if err != nil {
		s.writeStorageErr(w, err)
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", info.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	_, _ = io.Copy(w, reader)
}

func (s *Server) handlePutChunk(w http.ResponseWriter, r *http.Request, roomID, transferID, rawIndex string) {
	index, err := strconv.Atoi(rawIndex)
	if err != nil || index < 0 {
		http.Error(w, "invalid chunk index", http.StatusBadRequest)
		return
	}
	body := http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadSize)
	transfer, err := s.roomService.PutChunk(r.Context(), roomID, transferID, index, r.Header.Get("X-Checksum-Sha256"), body, r.ContentLength)
	if err != nil {
		s.writeStorageErr(w, err)
		return
	}
	s.broadcastEvent(domain.RoomEvent{
		Type:       "transfer.updated",
		Room:       roomID,
		TransferID: transferID,
		Status:     transfer.Status,
		CreatedAt:  time.Now().UTC(),
		Data:       transfer,
	})
	s.broadcastSnapshot(r.Context(), roomID)
	s.writeJSON(w, http.StatusAccepted, map[string]string{"status": "chunk stored"})
}

func (s *Server) handleGetChunk(w http.ResponseWriter, r *http.Request, roomID, transferID, rawIndex string) {
	index, err := strconv.Atoi(rawIndex)
	if err != nil || index < 0 {
		http.Error(w, "invalid chunk index", http.StatusBadRequest)
		return
	}
	actor := strings.TrimSpace(r.URL.Query().Get("actor"))
	if actor == "" {
		actor = "Anonymous"
	}
	if index == 0 {
		if _, err := s.roomService.MarkDownloading(r.Context(), roomID, transferID, actor); err != nil && !errors.Is(err, meta.ErrNotFound) {
			s.logger.Warn("mark downloading failed", "room", roomID, "transfer", transferID, "err", err)
		}
	}
	reader, info, err := s.roomService.GetChunk(r.Context(), roomID, transferID, index)
	if err != nil {
		s.writeStorageErr(w, err)
		return
	}
	defer reader.Close()
	s.broadcastSnapshot(r.Context(), roomID)
	w.Header().Set("Content-Type", info.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	_, _ = io.Copy(w, reader)
}

func (s *Server) handleRoomWS(w http.ResponseWriter, r *http.Request) {
	roomID := strings.TrimPrefix(r.URL.Path, "/ws/rooms/")
	if roomID == "" {
		http.NotFound(w, r)
		return
	}
	peer := strings.TrimSpace(r.URL.Query().Get("peer"))
	if peer == "" {
		peer = "Anonymous"
	}
	if !s.wsHub.ServeHTTP(w, r, roomID, peer, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.roomService.AppendEvent(ctx, domain.RoomEvent{
			ID:        fmt.Sprintf("left_%d", time.Now().UnixNano()),
			Type:      "peer.left",
			Room:      roomID,
			Actor:     peer,
			Message:   peer + " 离开房间",
			CreatedAt: time.Now().UTC(),
		})
		s.broadcastSnapshot(ctx, roomID)
	}) {
		return
	}
	_ = s.roomService.AppendEvent(r.Context(), domain.RoomEvent{
		ID:        fmt.Sprintf("join_%d", time.Now().UnixNano()),
		Type:      "peer.joined",
		Room:      roomID,
		Actor:     peer,
		Message:   peer + " 进入房间",
		CreatedAt: time.Now().UTC(),
	})
	s.broadcastSnapshot(r.Context(), roomID)
}

func (s *Server) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.roomService.CleanupExpired(ctx); err != nil {
				s.logger.Warn("cleanup expired rooms failed", "err", err)
			}
		}
	}
}

func (s *Server) broadcastSnapshot(ctx context.Context, roomID string) {
	snapshot, err := s.roomService.GetRoomSnapshot(ctx, roomID, s.wsHub.OnlineCount(roomID))
	if err != nil {
		s.logger.Warn("build snapshot failed", "room", roomID, "err", err)
		return
	}
	s.broadcastEvent(domain.RoomEvent{
		ID:        fmt.Sprintf("snap_%d", time.Now().UnixNano()),
		Type:      "room.snapshot",
		Room:      roomID,
		Message:   "room snapshot",
		CreatedAt: time.Now().UTC(),
		Data:      snapshot,
	})
}

func (s *Server) broadcastEvent(event domain.RoomEvent) {
	s.wsHub.Broadcast(event.Room, event)
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) writeMetaErr(w http.ResponseWriter, err error) {
	if errors.Is(err, meta.ErrNotFound) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func (s *Server) writeStorageErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrObjectNotFound), errors.Is(err, meta.ErrNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func ensureTLSMaterial(certPath, keyPath string) error {
	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			return nil
		}
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{
			CommonName: "gossh.local",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return fmt.Errorf("create certificate: %w", err)
	}

	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes}), 0o644); err != nil {
		return fmt.Errorf("write cert: %w", err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)}), 0o600); err != nil {
		return fmt.Errorf("write key: %w", err)
	}
	return nil
}

func modeFromWebRTC(enabled bool) string {
	if enabled {
		return "webrtc-auto-fallback"
	}
	return "relay-first"
}

func isLoopbackHost(hostport string) bool {
	host := hostWithoutPort(hostport)
	if host == "" {
		host = hostport
	}

	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}

	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func hostWithoutPort(hostport string) string {
	host, _, err := net.SplitHostPort(hostport)
	if err == nil {
		return host
	}
	return strings.Trim(hostport, "[]")
}

func portOrDefault(addr, fallback string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return fallback
	}
	return port
}
