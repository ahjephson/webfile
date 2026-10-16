package httpserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"webfile/internal/filesystem"
	"webfile/internal/operations"
	"webfile/internal/watch"
)

const (
	websocketReadLimit    = 64 << 10
	websocketWriteTimeout = 5 * time.Second
	websocketPingInterval = 30 * time.Second
)

type websocketClientMessage struct {
	Type        string `json:"type"`
	Path        string `json:"path,omitempty"`
	OperationID string `json:"operation_id,omitempty"`
}

type websocketServerMessage struct {
	Type         string                        `json:"type"`
	Path         string                        `json:"path,omitempty"`
	Reason       string                        `json:"reason,omitempty"`
	At           time.Time                     `json:"at,omitempty"`
	Code         string                        `json:"code,omitempty"`
	Message      string                        `json:"message,omitempty"`
	Operation    *operations.Operation         `json:"operation,omitempty"`
	ThumbnailJob *filesystem.VideoThumbnailJob `json:"thumbnail_job,omitempty"`
}

// websocket is the worker's realtime channel. The browser uses ordinary HTTP
// for filesystem operations and this connection for invalidation/progress
// messages. Directory notifications deliberately mean only "your view may be
// stale": the browser re-reads the directory and the filesystem remains the
// source of truth.
func (s *Server) websocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		slog.Debug("websocket accept failed", slog.String("err", err.Error()))
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(websocketReadLimit)

	// coder/websocket recommends not carrying r.Context() beyond the HTTP
	// upgrade. The read loop below owns this connection context and cancels it
	// when either side goes away.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	inbound := make(chan websocketClientMessage, 8)
	readErr := make(chan error, 1)
	go readWebsocketMessages(ctx, conn, inbound, readErr)

	var (
		watchedPath string
		events      <-chan watch.Event
		cancelWatch func()
	)
	defer func() {
		if cancelWatch != nil {
			cancelWatch()
		}
	}()

	opEvents, cancelOps := s.ops.Subscribe()
	defer cancelOps()
	thumbnailEvents, cancelThumbnails := s.files.SubscribeVideoThumbnails()
	defer cancelThumbnails()

	if err := writeWebsocketMessage(ctx, conn, websocketServerMessage{Type: "ready"}); err != nil {
		return
	}

	ping := time.NewTicker(websocketPingInterval)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case err := <-readErr:
			if err != nil {
				status := websocket.CloseStatus(err)
				if status != websocket.StatusNormalClosure && status != websocket.StatusGoingAway {
					slog.Debug("websocket closed", slog.Int("status", int(status)), slog.String("err", err.Error()))
				}
			}
			return

		case message, ok := <-inbound:
			if !ok {
				return
			}
			switch message.Type {
			case "subscribe-directory":
				clean := filesystem.CleanVirtual(message.Path)
				if clean == "/" {
					_ = writeWebsocketMessage(ctx, conn, websocketServerMessage{
						Type: "error", Code: "invalid_path", Message: "the virtual root is not watchable",
					})
					continue
				}
				if clean == watchedPath && events != nil {
					_ = writeWebsocketMessage(ctx, conn, websocketServerMessage{Type: "subscribed", Path: clean})
					continue
				}

				nextEvents, nextCancel, err := s.subscribeDirectory(clean)
				if err != nil {
					_ = writeWebsocketMessage(ctx, conn, websocketServerMessage{
						Type: "error", Path: clean, Code: "subscribe_failed", Message: err.Error(),
					})
					continue
				}
				if cancelWatch != nil {
					cancelWatch()
				}
				watchedPath = clean
				events = nextEvents
				cancelWatch = nextCancel
				if err := writeWebsocketMessage(ctx, conn, websocketServerMessage{Type: "subscribed", Path: clean}); err != nil {
					return
				}

			case "unsubscribe-directory":
				if cancelWatch != nil {
					cancelWatch()
					cancelWatch = nil
				}
				watchedPath = ""
				events = nil
				if err := writeWebsocketMessage(ctx, conn, websocketServerMessage{Type: "unsubscribed"}); err != nil {
					return
				}

			case "cancel-operation":
				if !s.ops.Cancel(message.OperationID) {
					_ = writeWebsocketMessage(ctx, conn, websocketServerMessage{
						Type: "error", Code: "cancel_failed", Message: "operation is not running",
					})
				}

			default:
				_ = writeWebsocketMessage(ctx, conn, websocketServerMessage{
					Type: "error", Code: "unknown_message", Message: "unknown websocket message type",
				})
			}

		case operation, ok := <-opEvents:
			if !ok {
				opEvents = nil
				continue
			}
			if err := writeWebsocketMessage(ctx, conn, websocketServerMessage{Type: "operation-updated", Operation: &operation}); err != nil {
				return
			}

		case job, ok := <-thumbnailEvents:
			if !ok {
				thumbnailEvents = nil
				continue
			}
			if err := writeWebsocketMessage(ctx, conn, websocketServerMessage{Type: "thumbnail-job-updated", ThumbnailJob: &job}); err != nil {
				return
			}

		case event, ok := <-events:
			if !ok {
				events = nil
				cancelWatch = nil
				watchedPath = ""
				continue
			}
			s.files.InvalidateFolderSizes(watchedPath)
			if err := writeWebsocketMessage(ctx, conn, websocketServerMessage{
				Type: "directory-invalidated", Path: watchedPath, Reason: event.Kind, At: event.At,
			}); err != nil {
				return
			}

		case <-ping.C:
			pingCtx, pingCancel := context.WithTimeout(ctx, websocketWriteTimeout)
			err := conn.Ping(pingCtx)
			pingCancel()
			if err != nil {
				return
			}
		}
	}
}

func (s *Server) subscribeDirectory(virtualPath string) (<-chan watch.Event, func(), error) {
	_, absolutePath, err := s.files.Resolver().ResolveExisting(virtualPath)
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(absolutePath)
	if err != nil {
		return nil, nil, err
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("%w: %s", filesystem.ErrNotDirectory, virtualPath)
	}
	return s.watch.Subscribe(absolutePath)
}

func readWebsocketMessages(ctx context.Context, conn *websocket.Conn, inbound chan<- websocketClientMessage, readErr chan<- error) {
	defer close(inbound)
	for {
		var message websocketClientMessage
		if err := wsjson.Read(ctx, conn, &message); err != nil {
			select {
			case readErr <- err:
			default:
			}
			return
		}
		select {
		case inbound <- message:
		case <-ctx.Done():
			return
		}
	}
}

func writeWebsocketMessage(ctx context.Context, conn *websocket.Conn, message websocketServerMessage) error {
	writeCtx, cancel := context.WithTimeout(ctx, websocketWriteTimeout)
	defer cancel()
	return wsjson.Write(writeCtx, conn, message)
}
