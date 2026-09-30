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
	if len(target.thumbnails.items) != 1 {
		t.Fatalf("cache contains %d entries", len(target.thumbnails.items))
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
	if !strings.Contains(argumentText, "thumbnail=5") || !strings.Contains(argumentText, "tile=10x1:nb_frames=10") {
		t.Fatalf("representative sprite filter was not used: %q", argumentText)
	}
	if !strings.Contains(argumentText, "scale=w=320:h=180:force_original_aspect_ratio=decrease") {
		t.Fatalf("requested thumbnail bounds were not used: %q", argumentText)
	}
}

func TestVideoSpriteFilterSkipsOpeningAndClosingFivePercent(t *testing.T) {
	filter := videoSpriteFilter(200, 320, 180)
	if !strings.Contains(filter, "trim=start=10.000000:end=190.000000") {
		t.Fatalf("unexpected trim: %q", filter)
	}
	if !strings.Contains(filter, "fps=0.277777778") {
		t.Fatalf("unexpected sample rate: %q", filter)
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
