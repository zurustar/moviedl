// ffmpeg_test.go: ffmpeg.go の例示ベーステスト。PBT は pbt_test.go に分離している（PBT-10）。

package main

import "testing"

// 仕様: aidlc-docs/inception/application-design/design.md「Windows ffmpeg の取得」
// zip エントリ名一覧から basename が ffmpeg.exe のエントリを返す。
func TestFfmpegZipEntry(t *testing.T) {
	t.Run("bin/ffmpeg.exe を選ぶ", func(t *testing.T) {
		names := []string{
			"ffmpeg-master-latest-win64-gpl/",
			"ffmpeg-master-latest-win64-gpl/bin/",
			"ffmpeg-master-latest-win64-gpl/bin/ffprobe.exe",
			"ffmpeg-master-latest-win64-gpl/bin/ffmpeg.exe",
			"ffmpeg-master-latest-win64-gpl/LICENSE.txt",
		}
		got, err := ffmpegZipEntry(names)
		if err != nil {
			t.Fatal(err)
		}
		if got != "ffmpeg-master-latest-win64-gpl/bin/ffmpeg.exe" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("ffmpeg.exe が無ければエラー", func(t *testing.T) {
		names := []string{"x/bin/ffprobe.exe", "x/bin/notffmpeg.exe"}
		if _, err := ffmpegZipEntry(names); err == nil {
			t.Error("エラーになるべき")
		}
	})

	t.Run("末尾一致の誤検出をしない", func(t *testing.T) {
		// basename が "myffmpeg.exe" は ffmpeg.exe ではない
		names := []string{"x/bin/myffmpeg.exe"}
		if _, err := ffmpegZipEntry(names); err == nil {
			t.Error("myffmpeg.exe を誤って採用した")
		}
	})
}
