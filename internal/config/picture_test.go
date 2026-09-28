package config

import (
	"math"
	"strings"
	"testing"
)

func TestPictureRect(t *testing.T) {
	cases := []struct {
		name       string
		video      VideoConfig
		outW, outH int
		interlaced bool
		want       PictureRect
	}{
		{"unset is full raster", VideoConfig{}, 720, 480, true, PictureRect{0, 0, 720, 480}},
		{"defaults are full raster", VideoConfig{PictureHSize: 100, PictureVSize: 100}, 720, 240, false, PictureRect{0, 0, 720, 240}},
		{"shrink centres", VideoConfig{PictureHSize: 90, PictureVSize: 90}, 720, 480, true, PictureRect{36, 24, 648, 432}},
		{"sizes round to even", VideoConfig{PictureHSize: 92.5, PictureVSize: 92.5}, 720, 240, false, PictureRect{27, 9, 666, 222}},
		// 480*0.955 = 458.4 -> 458; centring gives y=11, forced even on interlaced.
		{"interlaced keeps y even", VideoConfig{PictureVSize: 95.5}, 720, 480, true, PictureRect{0, 10, 720, 458}},
		{"interlaced vertical offset is field lines", VideoConfig{PictureVSize: 90, PictureVOffset: 3}, 720, 480, true, PictureRect{0, 30, 720, 432}},
		{"progressive vertical offset is raster lines", VideoConfig{PictureVSize: 90, PictureVOffset: -3}, 720, 240, false, PictureRect{0, 9, 720, 216}},
		{"horizontal offset at full size runs off-raster", VideoConfig{PictureHOffset: -10}, 720, 480, true, PictureRect{-10, 0, 720, 480}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.video.PictureRect(tc.outW, tc.outH, tc.interlaced)
			if got != tc.want {
				t.Fatalf("PictureRect = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestPictureRectVisibleAndIsFull(t *testing.T) {
	r := PictureRect{X: -10, Y: 4, W: 720, H: 480}
	if got, want := r.Visible(720, 480), (PictureRect{X: 0, Y: 4, W: 710, H: 476}); got != want {
		t.Fatalf("Visible = %+v, want %+v", got, want)
	}
	if got := (PictureRect{X: 800, W: 720, H: 480}).Visible(720, 480); got != (PictureRect{}) {
		t.Fatalf("off-screen Visible = %+v, want empty", got)
	}
	if !(PictureRect{W: 720, H: 480}).IsFull(720, 480) {
		t.Fatal("full raster should report IsFull")
	}
	if r.IsFull(720, 480) {
		t.Fatal("shifted rect should not report IsFull")
	}
}

func TestValidatePicture(t *testing.T) {
	cases := []struct {
		name    string
		video   VideoConfig
		wantErr string
	}{
		{"unset", VideoConfig{}, ""},
		{"bounds", VideoConfig{PictureHSize: 80, PictureVSize: 100, PictureHOffset: -72, PictureVOffset: 28}, ""},
		{"h size too small", VideoConfig{PictureHSize: 79.5}, "picture_h_size"},
		{"v size too big", VideoConfig{PictureVSize: 101}, "picture_v_size"},
		{"v size NaN", VideoConfig{PictureVSize: math.NaN()}, "picture_v_size"},
		{"h offset", VideoConfig{PictureHOffset: 73}, "picture_h_offset"},
		{"v offset", VideoConfig{PictureVOffset: -29}, "picture_v_offset"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePicture(tc.video)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want mention of %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadSectionedPictureDefaults(t *testing.T) {
	s, _, err := loadSectionedFromBytes([]byte("[bridge.video]\npicture_h_size = 92.5\npicture_v_offset = -2\n"))
	if err != nil {
		t.Fatal(err)
	}
	v := s.Bridge.Video
	if v.PictureHSize != 92.5 || v.PictureVSize != DefaultPictureSize || v.PictureHOffset != 0 || v.PictureVOffset != -2 {
		t.Fatalf("decoded picture fields = %+v", v)
	}
}
