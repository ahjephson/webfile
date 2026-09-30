package filesystem

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/disintegration/imaging"
)

const maxThumbnailPixels = 25_000_000

const (
	videoThumbnailFrames     = 10
	videoThumbnailCandidates = 5
)

var ErrThumbnailTooLarge = errors.New("image is too large to thumbnail")

type Thumbnail struct {
	Data        []byte
	ContentType string
	ETag        string
}

type videoThumbnailGenerator struct {
	ffmpeg  string
	ffprobe string
}

type thumbnailCacheEntry struct {
	key       string
	thumbnail Thumbnail
}

type thumbnailCache struct {
	mu       sync.Mutex
	maxBytes int
	bytes    int
	items    map[string]*list.Element
	order    *list.List
}

func newThumbnailCache(maxBytes int) *thumbnailCache {
	return &thumbnailCache{
		maxBytes: maxBytes,
		items:    make(map[string]*list.Element),
		order:    list.New(),
	}
}

func (c *thumbnailCache) get(key string) (Thumbnail, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.items[key]
	if !ok {
		return Thumbnail{}, false
	}
	c.order.MoveToFront(element)
	return element.Value.(*thumbnailCacheEntry).thumbnail, true
}

func (c *thumbnailCache) put(key string, thumbnail Thumbnail) {
	if len(thumbnail.Data) > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.items[key]; ok {
		entry := existing.Value.(*thumbnailCacheEntry)
		c.bytes -= len(entry.thumbnail.Data)
		entry.thumbnail = thumbnail
		c.bytes += len(thumbnail.Data)
		c.order.MoveToFront(existing)
	} else {
		entry := &thumbnailCacheEntry{key: key, thumbnail: thumbnail}
		c.items[key] = c.order.PushFront(entry)
		c.bytes += len(thumbnail.Data)
	}
	for c.bytes > c.maxBytes {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		entry := oldest.Value.(*thumbnailCacheEntry)
		delete(c.items, entry.key)
		c.bytes -= len(entry.thumbnail.Data)
		c.order.Remove(oldest)
	}
}

func (s *Service) Thumbnail(ctx context.Context, virtualPath string, maxWidth, maxHeight int) (Thumbnail, error) {
	if maxWidth <= 0 || maxHeight <= 0 {
		return Thumbnail{}, errors.New("thumbnail dimensions must be positive")
	}
	file, info, err := s.OpenDownload(ctx, virtualPath)
	if err != nil {
		return Thumbnail{}, err
	}
	defer file.Close()

	key := fmt.Sprintf("thumbnail-v2:%s:%d:%d:%dx%d", CleanVirtual(virtualPath), info.Size(), info.ModTime().UnixNano(), maxWidth, maxHeight)
	if thumbnail, ok := s.thumbnails.get(key); ok {
		return thumbnail, nil
	}
	select {
	case s.thumbnailSlots <- struct{}{}:
		defer func() { <-s.thumbnailSlots }()
	case <-ctx.Done():
		return Thumbnail{}, ctx.Err()
	}
	if thumbnail, ok := s.thumbnails.get(key); ok {
		return thumbnail, nil
	}

	mediaType, err := thumbnailMediaType(file)
	if err != nil {
		return Thumbnail{}, err
	}
	if strings.HasPrefix(mediaType, "video/") {
		data, err := s.videoThumbnails.generate(ctx, file.Name(), maxWidth, maxHeight)
		if err != nil {
			return Thumbnail{}, err
		}
		return s.cacheThumbnail(key, data, "image/webp"), nil
	}

	config, format, err := image.DecodeConfig(file)
	if err != nil {
		return Thumbnail{}, err
	}
	if int64(config.Width)*int64(config.Height) > maxThumbnailPixels {
		return Thumbnail{}, ErrThumbnailTooLarge
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Thumbnail{}, err
	}
	if err := ctx.Err(); err != nil {
		return Thumbnail{}, err
	}

	source, err := imaging.Decode(file, imaging.AutoOrientation(true))
	if err != nil {
		return Thumbnail{}, err
	}
	resized := imaging.Fit(source, maxWidth, maxHeight, imaging.Lanczos)
	var encoded bytes.Buffer
	contentType := "image/jpeg"
	if format == "png" || format == "gif" {
		contentType = "image/png"
		err = png.Encode(&encoded, resized)
	} else {
		err = jpeg.Encode(&encoded, resized, &jpeg.Options{Quality: 82})
	}
	if err != nil {
		return Thumbnail{}, err
	}
	if err := ctx.Err(); err != nil {
		return Thumbnail{}, err
	}

	return s.cacheThumbnail(key, encoded.Bytes(), contentType), nil
}

func (s *Service) cacheThumbnail(key string, data []byte, contentType string) Thumbnail {
	digest := sha256.Sum256([]byte(key))
	thumbnail := Thumbnail{Data: data, ContentType: contentType, ETag: fmt.Sprintf("\"%x\"", digest[:16])}
	s.thumbnails.put(key, thumbnail)
	return thumbnail
}

func thumbnailMediaType(file *os.File) (string, error) {
	sample := make([]byte, 512)
	n, err := file.Read(sample)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	contentType := http.DetectContentType(sample[:n])
	if extensionType := mime.TypeByExtension(filepath.Ext(file.Name())); strings.HasPrefix(extensionType, "video/") && contentType == "application/octet-stream" {
		contentType = extensionType
	}
	return strings.SplitN(contentType, ";", 2)[0], nil
}

func (g videoThumbnailGenerator) generate(ctx context.Context, path string, width, height int) ([]byte, error) {
	durationOutput, err := exec.CommandContext(ctx, g.ffprobe,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	).Output()
	if err != nil {
		return nil, fmt.Errorf("probe video duration: %w", err)
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(string(durationOutput)), 64)
	if err != nil || duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return nil, errors.New("probe video duration: invalid duration")
	}

	filter := videoSpriteFilter(duration, width, height)
	command := exec.CommandContext(ctx, g.ffmpeg,
		"-hide_banner", "-loglevel", "error",
		"-i", path,
		"-map", "0:v:0", "-an",
		"-vf", filter,
		"-frames:v", "1",
		"-c:v", "libwebp", "-quality", "82",
		"-f", "image2pipe", "pipe:1",
	)
	var output bytes.Buffer
	var errorOutput bytes.Buffer
	command.Stdout = &output
	command.Stderr = &errorOutput
	if err := command.Run(); err != nil {
		detail := strings.TrimSpace(errorOutput.String())
		if len(detail) > 1024 {
			detail = detail[:1024]
		}
		if detail != "" {
			return nil, fmt.Errorf("generate video thumbnail: %w: %s", err, detail)
		}
		return nil, fmt.Errorf("generate video thumbnail: %w", err)
	}
	if output.Len() == 0 {
		return nil, errors.New("generate video thumbnail: ffmpeg produced no image")
	}
	return output.Bytes(), nil
}

func videoSpriteFilter(duration float64, width, height int) string {
	start := duration * 0.05
	end := duration * 0.95
	sampleDuration := math.Max(end-start, 0.001)
	sampleRate := float64(videoThumbnailFrames*videoThumbnailCandidates) / sampleDuration
	return fmt.Sprintf(
		"trim=start=%.6f:end=%.6f,setpts=PTS-STARTPTS,fps=%.9f,thumbnail=%d,scale=w=%d:h=%d:force_original_aspect_ratio=decrease,pad=w=%d:h=%d:x=(ow-iw)/2:y=(oh-ih)/2:color=black,tile=%dx1:nb_frames=%d",
		start, end, sampleRate, videoThumbnailCandidates, width, height, width, height, videoThumbnailFrames, videoThumbnailFrames,
	)
}
