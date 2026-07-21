package deployer

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// ====== 进度持久化 ======

type DeployProgress struct {
	NodeID      string `json:"node_id"`
	CurrentStage int    `json:"current_stage"`
	StageName   string `json:"stage_name"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	StartedAt   int64  `json:"started_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

type ProgressStore struct {
	baseDir string
}

func NewProgressStore(baseDir string) *ProgressStore {
	os.MkdirAll(baseDir, 0755)
	return &ProgressStore{baseDir: baseDir}
}

func (s *ProgressStore) Save(p *DeployProgress) error {
	p.UpdatedAt = time.Now().UnixMilli()
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.baseDir, fmt.Sprintf("deploy_%s.json", p.NodeID))
	return os.WriteFile(path, data, 0644)
}

func (s *ProgressStore) Load(nodeID string) (*DeployProgress, error) {
	path := filepath.Join(s.baseDir, fmt.Sprintf("deploy_%s.json", nodeID))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p DeployProgress
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *ProgressStore) CanResume(nodeID string) (int, bool) {
	p, err := s.Load(nodeID)
	if err != nil {
		return 0, false
	}
	if p.Status == "failed" {
		return p.CurrentStage, true
	}
	return 0, false
}

// ====== SSE 处理 ======

type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func NewSSEWriter(w http.ResponseWriter) *SSEWriter {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil
	}
	flusher.Flush()

	return &SSEWriter{w: w, flusher: flusher}
}

func (s *SSEWriter) Send(entry LogEntry) {
	data, _ := json.Marshal(entry)
	fmt.Fprintf(s.w, "data: %s\n\n", data)
	s.flusher.Flush()
}

func (s *SSEWriter) Heartbeat() {
	fmt.Fprintf(s.w, ": heartbeat\n\n")
	s.flusher.Flush()
}
