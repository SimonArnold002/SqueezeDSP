package LyrionDSPSettings

import (
	"os"
	"testing"
)

// TestTrackURLKeepsItsCase: the track's URL reaches the engine exactly as LMS gave it (THE GAPLESS ROUND, found live
// 2026-11-05): the next track's engine matches it to the plugin's note byte for byte, and the parser lowercased every
// "--name=value" whole, so every URL with a capital (a local file's folders, a Qobuz stream's signature, the hex of a
// non-ASCII escape) never matched and no join carried. Both forms the plugin's rules could use, URLs as LMS sends them
// (escapes, "&", "=", raw UTF-8); the other values still lowercased, as Fox's code expects.
func TestTrackURLKeepsItsCase(t *testing.T) {
	saved := os.Args
	defer func() { os.Args = saved }()
	for _, url := range []string{
		"file:///mnt/dietpi_userdata/Music/Music/flac/Bobbie%20Gentry/Bobby%20Gentry%20&%20Glen%20Campbell/5-My%20Elusive%20Dreams.flac",
		"https://streaming-qobuz-std.akamaized.net/file?uid=355122&eid=193171335&fmt=27&profile=raw&hmac=rBXCRr_XzOqIm-QjKcA2jK8gUaU",
		"file:///Music/Bj%C3%B6rk/Homog%C3%A9nic/01-Hunter.flac",
		"file:///Music/Björk/Homogénic/01-Hunter.flac",
		"qobuz://193171335.flac",
		// Simon's Zalgo album, as LMS holds it (Simon: "this one needs to work")
		"file:///mnt/dietpi_userdata/Music/Music/flac/%E2%A3%8E%E2%A1%87%EA%89%BA%E1%83%9A%E0%BC%BD%E0%AE%87%E2%80%A2%CC%9B)%E0%BE%80%E2%97%9E%20%E0%BC%8E%E0%BA%B6%20%E0%BC%BD%E0%A7%A3%E0%A7%A2%D8%9E%E0%A7%A2%D8%9E%D8%96%20%EA%89%BA%E1%83%9A/1.flac",
	} {
		for _, form := range [][]string{{"--trackURL=" + url}, {"--trackURL", url}} {
			os.Args = append([]string{"SqueezeDSP", "--Clientid=cd-ef-e3-a9-15-31", "--formatin=DSD"}, form...)
			var a Arguments
			if err := parseArgs(&a); err != nil {
				t.Fatalf("%q: %v", form, err)
			}
			if a.TrackURL != url {
				t.Errorf("%q: TrackURL %q, want it as given", form, a.TrackURL)
			}
			if a.InputFormat != "dsd" || a.CleanClientID == "" {
				t.Errorf("%q: the other values changed: formatin %q, clean client %q", form, a.InputFormat, a.CleanClientID)
			}
		}
	}
}
