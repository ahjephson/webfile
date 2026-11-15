package thumbnailsettings

import "errors"

// Settings controls generation for one user's worker. Cache storage is server-wide.
type Settings struct {
	ImagesEnabled       bool `json:"images_enabled"`
	VideosEnabled       bool `json:"videos_enabled"`
	ImageWorkers        int  `json:"image_workers"`
	VideoWorkers        int  `json:"video_workers"`
	VideoFFmpegThreads  int  `json:"video_ffmpeg_threads"`
	VideoTimeoutSeconds int  `json:"video_timeout_seconds"`
	MaxQueued           int  `json:"max_queued"`
}

func Default() Settings {
	return Settings{ImagesEnabled: true, VideosEnabled: true, ImageWorkers: 2, VideoWorkers: 2, VideoFFmpegThreads: 2, VideoTimeoutSeconds: 300, MaxQueued: 200}
}

func (s Settings) Validate() error {
	if s.ImageWorkers < 1 || s.ImageWorkers > 32 || s.VideoWorkers < 1 || s.VideoWorkers > 32 || s.VideoFFmpegThreads < 1 || s.VideoFFmpegThreads > 32 {
		return errors.New("thumbnail worker and thread counts must be between 1 and 32")
	}
	if s.VideoTimeoutSeconds < 1 || s.VideoTimeoutSeconds > 3600 {
		return errors.New("video thumbnail timeout must be between 1 and 3600 seconds")
	}
	if s.MaxQueued < 1 || s.MaxQueued > 10000 {
		return errors.New("thumbnail queue limit must be between 1 and 10000")
	}
	return nil
}

const Header = "X-WebFile-Thumbnail-Settings"
