package adapters

import "fmt"

// FormatSeasonEpisode renders a TV detail string for the VFD tertiary
// row: "S04E05", "S04", "E05", optionally suffixed " · YYYY". Zero
// components are omitted. With no S/E but a year, returns the year alone
// ("2017"); used for movies too. Empty when everything is zero.
func FormatSeasonEpisode(season, episode, year int) string {
	var se string
	switch {
	case season > 0 && episode > 0:
		se = fmt.Sprintf("S%02dE%02d", season, episode)
	case season > 0:
		se = fmt.Sprintf("S%02d", season)
	case episode > 0:
		se = fmt.Sprintf("E%02d", episode)
	}
	switch {
	case se != "" && year > 0:
		return fmt.Sprintf("%s · %d", se, year)
	case se != "":
		return se
	case year > 0:
		return fmt.Sprintf("%d", year)
	default:
		return ""
	}
}

// FormatUploadDate converts yt-dlp's "YYYYMMDD" to ISO "YYYY-MM-DD".
// Malformed or empty input returns "" (the tertiary row then collapses).
func FormatUploadDate(raw string) string {
	if len(raw) != 8 {
		return ""
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return raw[0:4] + "-" + raw[4:6] + "-" + raw[6:8]
}
