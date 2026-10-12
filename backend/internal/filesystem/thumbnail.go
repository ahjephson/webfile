package filesystem

import (
	"bytes"
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
	"syscall"
	"time"

	"github.com/disintegration/imaging"

	"webfile/internal/thumbnailcache"
)

const maxThumbnailPixels = 25_000_000

const (
	videoThumbnailFrames   = 10
	thumbnailFormatVersion = "thumbnail-v4"
)

var ErrThumbnailTooLarge = errors.New("image is too large to thumbnail")

type Thumbnail struct {
	Data        []byte
	ContentType string
	ETag        string
}

type ThumbnailResult struct {
	Thumbnail *Thumbnail         `json:"-"`
	Job       *VideoThumbnailJob `json:"job,omitempty"`
	key       string
}

type ThumbnailOptions struct {
	Cache              thumbnailcache.Cache
	ImageWorkers       int
	VideoWorkers       int
	VideoFFmpegThreads int
	VideoTimeout       time.Duration
	MaxQueued          int
}

func (o *ThumbnailOptions) applyDefaults() {
	if o.Cache == nil {
		o.Cache = newMemoryThumbnailCache()
	}
	if o.ImageWorkers <= 0 {
		o.ImageWorkers = 2
	}
	if o.VideoWorkers <= 0 {
		o.VideoWorkers = 2
	}
	if o.VideoFFmpegThreads <= 0 {
		o.VideoFFmpegThreads = 2
	}
	if o.VideoTimeout <= 0 {
		o.VideoTimeout = 5 * time.Minute
	}
	if o.MaxQueued <= 0 {
		o.MaxQueued = 200
	}
}

type videoThumbnailGenerator struct {
	ffmpeg  string
	ffprobe string
	threads int
}

type thumbnailSource struct {
	virtualPath string
	absolute    string
	key         string
	mediaType   string
	contentType string
	width       int
	height      int
}

type thumbnailFlight struct {
	done      chan struct{}
	thumbnail Thumbnail
	err       error
}

func (s *Service) RequestThumbnail(ctx context.Context, virtualPath string, maxWidth, maxHeight int) (ThumbnailResult, error) {
	source, file, err := s.inspectThumbnailSource(ctx, virtualPath, maxWidth, maxHeight)
	if err != nil {
		return ThumbnailResult{}, err
	}
	defer file.Close()

	if thumbnail, ok, err := s.cachedThumbnail(ctx, source); err != nil {
		return ThumbnailResult{}, err
	} else if ok {
		return ThumbnailResult{Thumbnail: &thumbnail, key: source.key}, nil
	}

	if strings.HasPrefix(source.mediaType, "video/") {
		job, err := s.videoQueue.schedule(source)
		if err != nil {
			return ThumbnailResult{}, err
		}
		return ThumbnailResult{Job: &job, key: source.key}, nil
	}

	thumbnail, err := s.generateImageDeduplicated(ctx, file, source)
	if err != nil {
		return ThumbnailResult{}, err
	}
	return ThumbnailResult{Thumbnail: &thumbnail, key: source.key}, nil
}

// QueueVideoThumbnails inspects and enqueues video paths as one ordered batch.
// Workers are only woken after every source has been inserted, so the order
// supplied by the directory listing is preserved even when requests would
// otherwise arrive concurrently from multiple tiles.
func (s *Service) QueueVideoThumbnails(ctx context.Context, virtualPaths []string, maxWidth, maxHeight int) ([]VideoThumbnailJob, error) {
	sources := make([]thumbnailSource, 0, len(virtualPaths))
	seen := make(map[string]struct{}, len(virtualPaths))
	for _, virtualPath := range virtualPaths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		virtualPath = CleanVirtual(virtualPath)
		if _, ok := seen[virtualPath]; ok {
			continue
		}
		seen[virtualPath] = struct{}{}

		source, file, err := s.inspectThumbnailSource(ctx, virtualPath, maxWidth, maxHeight)
		if err != nil {
			continue
		}
		_ = file.Close()
		if !strings.HasPrefix(source.mediaType, "video/") {
			continue
		}
		if _, ok, err := s.cachedThumbnail(ctx, source); err != nil {
			return nil, err
		} else if ok {
			continue
		}
		sources = append(sources, source)
	}
	return s.videoQueue.scheduleBatch(sources), nil
}

func (s *Service) generateImageDeduplicated(ctx context.Context, file *os.File, source thumbnailSource) (Thumbnail, error) {
	s.thumbnailFlightMu.Lock()
	if existing := s.thumbnailFlights[source.key]; existing != nil {
		s.thumbnailFlightMu.Unlock()
		select {
		case <-existing.done:
			return existing.thumbnail, existing.err
		case <-ctx.Done():
			return Thumbnail{}, ctx.Err()
		}
	}
	flight := &thumbnailFlight{done: make(chan struct{})}
	s.thumbnailFlights[source.key] = flight
	s.thumbnailFlightMu.Unlock()

	select {
	case s.thumbnailSlots <- struct{}{}:
		defer func() { <-s.thumbnailSlots }()
	case <-ctx.Done():
		flight.err = ctx.Err()
		s.finishThumbnailFlight(source.key, flight)
		return Thumbnail{}, flight.err
	}
	if thumbnail, ok, err := s.cachedThumbnail(ctx, source); err != nil {
		flight.err = err
	} else if ok {
		flight.thumbnail = thumbnail
	} else {
		flight.thumbnail, flight.err = s.generateImageThumbnail(ctx, file, source)
	}
	s.finishThumbnailFlight(source.key, flight)
	return flight.thumbnail, flight.err
}

func (s *Service) finishThumbnailFlight(key string, flight *thumbnailFlight) {
	s.thumbnailFlightMu.Lock()
	delete(s.thumbnailFlights, key)
	close(flight.done)
	s.thumbnailFlightMu.Unlock()
}

// Thumbnail preserves the blocking API used by non-HTTP callers and tests.
// Browser requests use RequestThumbnail so video generation never holds an
// image response open for the duration of FFmpeg work.
func (s *Service) Thumbnail(ctx context.Context, virtualPath string, maxWidth, maxHeight int) (Thumbnail, error) {
	result, err := s.RequestThumbnail(ctx, virtualPath, maxWidth, maxHeight)
	if err != nil {
		return Thumbnail{}, err
	}
	if result.Thumbnail != nil {
		return *result.Thumbnail, nil
	}
	if err := s.videoQueue.wait(ctx, result.key); err != nil {
		return Thumbnail{}, err
	}
	result, err = s.RequestThumbnail(ctx, virtualPath, maxWidth, maxHeight)
	if err != nil {
		return Thumbnail{}, err
	}
	if result.Thumbnail == nil {
		return Thumbnail{}, errors.New("video thumbnail did not become ready")
	}
	return *result.Thumbnail, nil
}

func (s *Service) inspectThumbnailSource(ctx context.Context, virtualPath string, width, height int) (thumbnailSource, *os.File, error) {
	if width <= 0 || height <= 0 {
		return thumbnailSource{}, nil, errors.New("thumbnail dimensions must be positive")
	}
	file, info, err := s.OpenDownload(ctx, virtualPath)
	if err != nil {
		return thumbnailSource{}, nil, err
	}
	mediaType, err := thumbnailMediaType(file)
	if err != nil {
		_ = file.Close()
		return thumbnailSource{}, nil, err
	}
	contentType := ""
	switch {
	case strings.HasPrefix(mediaType, "video/"):
		contentType = "image/webp"
	case mediaType == "image/png", mediaType == "image/gif":
		contentType = "image/png"
	case strings.HasPrefix(mediaType, "image/"):
		contentType = "image/jpeg"
	default:
		_ = file.Close()
		return thumbnailSource{}, nil, errors.New("file type does not support thumbnails")
	}
	identity := fmt.Sprintf("%s:%s:%d:%d:%d", thumbnailFormatVersion, file.Name(), info.Size(), info.ModTime().UnixNano(), info.Mode())
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		identity += fmt.Sprintf(":%d:%d:%d:%d", stat.Dev, stat.Ino, stat.Ctim.Sec, stat.Ctim.Nsec)
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s:%dx%d:%s", identity, width, height, contentType)))
	return thumbnailSource{
		virtualPath: CleanVirtual(virtualPath), absolute: file.Name(), key: fmt.Sprintf("%x", digest),
		mediaType: mediaType, contentType: contentType, width: width, height: height,
	}, file, nil
}

func (s *Service) cachedThumbnail(ctx context.Context, source thumbnailSource) (Thumbnail, bool, error) {
	item, ok, err := s.thumbnailCache.Get(ctx, source.key)
	if err != nil || !ok {
		return Thumbnail{}, ok, err
	}
	return Thumbnail{Data: item.Data, ContentType: item.ContentType, ETag: `"` + source.key[:32] + `"`}, true, nil
}

func (s *Service) storeThumbnail(ctx context.Context, source thumbnailSource, data []byte) (Thumbnail, error) {
	item := thumbnailcache.Item{Data: data, ContentType: source.contentType}
	if err := s.thumbnailCache.Put(ctx, source.key, item); err != nil {
		return Thumbnail{}, err
	}
	return Thumbnail{Data: data, ContentType: source.contentType, ETag: `"` + source.key[:32] + `"`}, nil
}

func (s *Service) generateImageThumbnail(ctx context.Context, file *os.File, source thumbnailSource) (Thumbnail, error) {
	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return Thumbnail{}, err
	}
	if int64(config.Width)*int64(config.Height) > maxThumbnailPixels {
		return Thumbnail{}, ErrThumbnailTooLarge
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Thumbnail{}, err
	}
	decoded, err := imaging.Decode(file, imaging.AutoOrientation(true))
	if err != nil {
		return Thumbnail{}, err
	}
	resized := imaging.Fit(decoded, source.width, source.height, imaging.Lanczos)
	var encoded bytes.Buffer
	if source.contentType == "image/png" {
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
	return s.storeThumbnail(ctx, source, encoded.Bytes())
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
	durationOutput, err := exec.CommandContext(ctx, g.ffprobe, "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path).Output()
	if err != nil {
		return nil, fmt.Errorf("probe video duration: %w", err)
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(string(durationOutput)), 64)
	if err != nil || duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return nil, errors.New("probe video duration: invalid duration")
	}
	output, detail, err := g.run(ctx, path, width, height, duration, false)
	if err != nil && ctx.Err() == nil && strings.Contains(strings.ToLower(detail), "invalid color space") {
		output, detail, err = g.run(ctx, path, width, height, duration, true)
	}
	if err != nil {
		if len(detail) > 1024 {
			detail = detail[:1024]
		}
		if detail != "" {
			return nil, fmt.Errorf("generate video thumbnail: %w: %s", err, detail)
		}
		return nil, fmt.Errorf("generate video thumbnail: %w", err)
	}
	if len(output) == 0 {
		return nil, errors.New("generate video thumbnail: ffmpeg produced no image")
	}
	return output, nil
}

func (g videoThumbnailGenerator) run(ctx context.Context, path string, width, height int, duration float64, assumeBT709 bool) ([]byte, string, error) {
	args := []string{"-hide_banner", "-loglevel", "error"}
	for _, timestamp := range videoThumbnailTimestamps(duration) {
		args = append(args, "-threads", "1", "-ss", strconv.FormatFloat(timestamp, 'f', 6, 64))
		if assumeBT709 {
			args = append(args, "-colorspace", "bt709", "-color_primaries", "bt709", "-color_trc", "bt709")
		}
		args = append(args, "-i", path)
	}
	args = append(args,
		"-filter_complex", videoSpriteFilter(width, height), "-map", "[sprite]", "-an", "-frames:v", "1",
		"-threads", strconv.Itoa(g.threads), "-c:v", "libwebp", "-quality", "82", "-f", "image2pipe", "pipe:1",
	)
	command := exec.CommandContext(ctx, g.ffmpeg, args...)
	var output, errorOutput bytes.Buffer
	command.Stdout, command.Stderr = &output, &errorOutput
	if err := command.Run(); err != nil {
		return nil, strings.TrimSpace(errorOutput.String()), err
	}
	return output.Bytes(), strings.TrimSpace(errorOutput.String()), nil
}

func videoThumbnailTimestamps(duration float64) []float64 {
	timestamps := make([]float64, videoThumbnailFrames)
	for index := range timestamps {
		fraction := 0.05 + 0.90*float64(index)/float64(videoThumbnailFrames-1)
		timestamps[index] = duration * fraction
	}
	return timestamps
}

func videoSpriteFilter(width, height int) string {
	parts := make([]string, 0, videoThumbnailFrames+1)
	inputs := strings.Builder{}
	for index := 0; index < videoThumbnailFrames; index++ {
		parts = append(parts, fmt.Sprintf("[%d:v:0]scale=w=%d:h=%d:force_original_aspect_ratio=decrease,pad=w=%d:h=%d:x=(ow-iw)/2:y=(oh-ih)/2:color=black,setsar=1[v%d]", index, width, height, width, height, index))
		fmt.Fprintf(&inputs, "[v%d]", index)
	}
	parts = append(parts, fmt.Sprintf("%shstack=inputs=%d:shortest=1[sprite]", inputs.String(), videoThumbnailFrames))
	return strings.Join(parts, ";")
}

type memoryThumbnailCache struct {
	mu    sync.Mutex
	items map[string]thumbnailcache.Item
}

func newMemoryThumbnailCache() *memoryThumbnailCache {
	return &memoryThumbnailCache{items: make(map[string]thumbnailcache.Item)}
}

func (c *memoryThumbnailCache) Get(_ context.Context, key string) (thumbnailcache.Item, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.items[key]
	return item, ok, nil
}

func (c *memoryThumbnailCache) Put(_ context.Context, key string, item thumbnailcache.Item) error {
	c.mu.Lock()
	c.items[key] = item
	c.mu.Unlock()
	return nil
}

func (c *memoryThumbnailCache) Move(_ context.Context, from, to string) error {
	c.mu.Lock()
	if item, ok := c.items[from]; ok {
		c.items[to] = item
		delete(c.items, from)
	}
	c.mu.Unlock()
	return nil
}

func (c *memoryThumbnailCache) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	delete(c.items, key)
	c.mu.Unlock()
	return nil
}
