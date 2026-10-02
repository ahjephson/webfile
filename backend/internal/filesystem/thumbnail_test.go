package filesystem

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestThumbnailCreatesBoundedImageAndInvalidatesChangedSource(t *testing.T) {
	target, root := testService(t)
	path := filepath.Join(root, "photo.jpg")
	writeThumbnailTestImage(t, path, color.RGBA{R: 255, A: 255})
	firstTime := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, firstTime, firstTime); err != nil {
		t.Fatal(err)
	}

	first, err := target.Thumbnail(context.Background(), "/Files/photo.jpg", 200, 200)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(first.Data))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 200 || decoded.Bounds().Dy() != 100 {
		t.Fatalf("thumbnail dimensions are %dx%d", decoded.Bounds().Dx(), decoded.Bounds().Dy())
	}
	if first.ContentType != "image/jpeg" || first.ETag == "" {
		t.Fatalf("unexpected thumbnail metadata: %#v", first)
	}
	memory := target.thumbnailCache.(*memoryThumbnailCache)
	if len(memory.items) != 1 {
		t.Fatalf("cache contains %d entries", len(memory.items))
	}

	writeThumbnailTestImage(t, path, color.RGBA{B: 255, A: 255})
	secondTime := firstTime.Add(time.Second)
	if err := os.Chtimes(path, secondTime, secondTime); err != nil {
		t.Fatal(err)
	}
	second, err := target.Thumbnail(context.Background(), "/Files/photo.jpg", 200, 200)
	if err != nil {
		t.Fatal(err)
	}
	if second.ETag == first.ETag {
		t.Fatal("changed source reused the old thumbnail ETag")
	}
}

func TestThumbnailCreatesAndCachesRepresentativeVideoSprite(t *testing.T) {
	target, root := testService(t)
	videoPath := filepath.Join(root, "clip.mp4")
	if err := os.WriteFile(videoPath, []byte{0, 1, 2, 3, 4, 5}, 0o600); err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	argsPath := filepath.Join(tools, "ffmpeg-args")
	probePath := filepath.Join(tools, "ffprobe")
	ffmpegPath := filepath.Join(tools, "ffmpeg")
	if err := os.WriteFile(probePath, []byte("#!/bin/sh\nprintf '100\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ffmpegScript := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$WEBFILE_TEST_FFMPEG_ARGS\"\nprintf 'sprite-data'\n"
	if err := os.WriteFile(ffmpegPath, []byte(ffmpegScript), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WEBFILE_TEST_FFMPEG_ARGS", argsPath)
	target.videoThumbnails = videoThumbnailGenerator{ffmpeg: ffmpegPath, ffprobe: probePath}

	first, err := target.Thumbnail(context.Background(), "/Files/clip.mp4", 320, 180)
	if err != nil {
		t.Fatal(err)
	}
	second, err := target.Thumbnail(context.Background(), "/Files/clip.mp4", 320, 180)
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Data) != "sprite-data" || first.ContentType != "image/webp" || first.ETag == "" {
		t.Fatalf("unexpected sprite: %#v", first)
	}
	if second.ETag != first.ETag {
		t.Fatal("cached video sprite changed ETag")
	}
	arguments, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	argumentText := string(arguments)
	if strings.Count(argumentText, "-hide_banner") != 1 {
		t.Fatalf("ffmpeg ran more than once: %q", argumentText)
	}
	if strings.Count(argumentText, "-ss\n") != videoThumbnailFrames || strings.Count(argumentText, "-i\n") != videoThumbnailFrames {
		t.Fatalf("fast seeks were not used for every sprite frame: %q", argumentText)
	}
	if !strings.Contains(argumentText, "5.000000") || !strings.Contains(argumentText, "95.000000") {
		t.Fatalf("sprite timestamps do not span the video: %q", argumentText)
	}
	if strings.Contains(argumentText, "trim=") || !strings.Contains(argumentText, "hstack=inputs=10:shortest=1") {
		t.Fatalf("fast-seek sprite filter was not used: %q", argumentText)
	}
	if !strings.Contains(argumentText, "scale=w=320:h=180:force_original_aspect_ratio=decrease") {
		t.Fatalf("requested thumbnail bounds were not used: %q", argumentText)
	}
}

func TestVideoThumbnailBatchPreservesSourceOrderBeforeWorkersStart(t *testing.T) {
	queue := newVideoThumbnailQueue(&Service{}, ThumbnailOptions{MaxQueued: 10, VideoTimeout: time.Minute})
	t.Cleanup(queue.close)
	sources := []thumbnailSource{
		{virtualPath: "/Files/first.mp4", key: "11111111111111111111111111111111"},
		{virtualPath: "/Files/second.mp4", key: "22222222222222222222222222222222"},
		{virtualPath: "/Files/third.mp4", key: "33333333333333333333333333333333"},
	}
	jobs := queue.scheduleBatch(sources)
	if len(jobs) != len(sources) {
		t.Fatalf("queued %d jobs, want %d", len(jobs), len(sources))
	}
	for index, source := range sources {
		if jobs[index].Path != source.virtualPath || jobs[index].QueuePosition != index+1 {
			t.Fatalf("job %d = %#v, want path %q at position %d", index, jobs[index], source.virtualPath, index+1)
		}
		if queue.queued[index].source.virtualPath != source.virtualPath {
			t.Fatalf("queue item %d = %q, want %q", index, queue.queued[index].source.virtualPath, source.virtualPath)
		}
	}
}

func TestVideoThumbnailRequestsAreQueuedAndDeduplicated(t *testing.T) {
	target, root := testService(t)
	videoPath := filepath.Join(root, "queued.mp4")
	if err := os.WriteFile(videoPath, []byte{0, 1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	probePath := filepath.Join(tools, "ffprobe")
	ffmpegPath := filepath.Join(tools, "ffmpeg")
	if err := os.WriteFile(probePath, []byte("#!/bin/sh\nprintf '30\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ffmpegPath, []byte("#!/bin/sh\nsleep 0.2\nprintf 'queued-sprite'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	target.videoThumbnails = videoThumbnailGenerator{ffmpeg: ffmpegPath, ffprobe: probePath, threads: 1}

	first, err := target.RequestThumbnail(context.Background(), "/Files/queued.mp4", 320, 180)
	if err != nil {
		t.Fatal(err)
	}
	second, err := target.RequestThumbnail(context.Background(), "/Files/queued.mp4", 320, 180)
	if err != nil {
		t.Fatal(err)
	}
	if first.Job == nil || second.Job == nil || first.Job.ID != second.Job.ID {
		t.Fatalf("requests were not deduplicated: first=%#v second=%#v", first.Job, second.Job)
	}
	if err := target.videoQueue.wait(context.Background(), first.key); err != nil {
		t.Fatal(err)
	}
	ready, err := target.RequestThumbnail(context.Background(), "/Files/queued.mp4", 320, 180)
	if err != nil {
		t.Fatal(err)
	}
	if ready.Thumbnail == nil || string(ready.Thumbnail.Data) != "queued-sprite" {
		t.Fatalf("unexpected ready thumbnail: %#v", ready.Thumbnail)
	}
}

func TestVideoThumbnailTimestampsSpanOpeningAndClosingFivePercent(t *testing.T) {
	timestamps := videoThumbnailTimestamps(200)
	if len(timestamps) != videoThumbnailFrames {
		t.Fatalf("got %d timestamps, want %d", len(timestamps), videoThumbnailFrames)
	}
	if timestamps[0] != 10 || timestamps[len(timestamps)-1] != 190 {
		t.Fatalf("timestamps span %.2f to %.2f, want 10 to 190", timestamps[0], timestamps[len(timestamps)-1])
	}
}

func writeThumbnailTestImage(t *testing.T, path string, fill color.Color) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 800, 400))
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			img.Set(x, y, fill)
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(file, img, &jpeg.Options{Quality: 90}); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
