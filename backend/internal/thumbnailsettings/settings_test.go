package thumbnailsettings

import "testing"

func TestGenerationSettingsBounds(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Settings){
		func(s *Settings) { s.VideoWorkers = 0 }, func(s *Settings) { s.ImageWorkers = 33 },
		func(s *Settings) { s.VideoFFmpegThreads = 33 }, func(s *Settings) { s.MaxQueued = 10001 },
		func(s *Settings) { s.VideoTimeoutSeconds = 3601 },
	} {
		settings := Default()
		change(&settings)
		if settings.Validate() == nil {
			t.Fatalf("invalid settings accepted: %#v", settings)
		}
	}
}
