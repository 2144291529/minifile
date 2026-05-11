package domain

import "time"

type RoomStatus string
type TransferStatus string

const (
	RoomStatusReady       RoomStatus = "ready"
	RoomStatusUploading   RoomStatus = "uploading"
	RoomStatusUploaded    RoomStatus = "uploaded"
	RoomStatusDownloading RoomStatus = "downloading"
	RoomStatusExpired     RoomStatus = "expired"
)

const (
	TransferStatusPending     TransferStatus = "pending"
	TransferStatusUploading   TransferStatus = "uploading"
	TransferStatusReady       TransferStatus = "ready"
	TransferStatusDownloading TransferStatus = "downloading"
)

type Room struct {
	ID             string     `json:"id"`
	Status         RoomStatus `json:"status"`
	StorageBackend string     `json:"storageBackend"`
	ExpiresAt      time.Time  `json:"expiresAt"`
	ChunkSize      int64      `json:"chunkSize"`
	TotalChunks    int        `json:"totalChunks"`
	UploadedChunks int        `json:"uploadedChunks"`
	BytesReceived  int64      `json:"bytesReceived"`
	ManifestSize   int64      `json:"manifestSize"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

type Transfer struct {
	ID             string         `json:"id"`
	RoomID         string         `json:"roomId"`
	Sender         string         `json:"sender"`
	Status         TransferStatus `json:"status"`
	ChunkSize      int64          `json:"chunkSize"`
	TotalChunks    int            `json:"totalChunks"`
	UploadedChunks int            `json:"uploadedChunks"`
	BytesReceived  int64          `json:"bytesReceived"`
	ManifestSize   int64          `json:"manifestSize"`
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
}

type Chunk struct {
	RoomID      string    `json:"roomId"`
	TransferID  string    `json:"transferId"`
	Index       int       `json:"index"`
	Size        int64     `json:"size"`
	Checksum    string    `json:"checksum"`
	ContentType string    `json:"contentType"`
	CreatedAt   time.Time `json:"createdAt"`
}

type ResumeState struct {
	RoomID         string    `json:"roomId"`
	TransferID     string    `json:"transferId"`
	ChunkSize      int64     `json:"chunkSize"`
	TotalChunks    int       `json:"totalChunks"`
	UploadedChunks []int     `json:"uploadedChunks"`
	Completed      bool      `json:"completed"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

type RoomEvent struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Room       string         `json:"room"`
	Actor      string         `json:"actor,omitempty"`
	TransferID string         `json:"transferId,omitempty"`
	Status     TransferStatus `json:"status,omitempty"`
	Message    string         `json:"message,omitempty"`
	CreatedAt  time.Time      `json:"createdAt"`
	Data       interface{}    `json:"data,omitempty"`
}

type RoomSnapshot struct {
	Room      Room        `json:"room"`
	Online    int         `json:"online"`
	Transfers []Transfer  `json:"transfers"`
	Events    []RoomEvent `json:"events"`
}
