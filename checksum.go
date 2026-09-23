// checksum.go: 取得したバイナリの SHA256 照合（yt-dlp / ffmpeg 共通）。

package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// fetchExpectedSum は SHA2-256SUMS を取得し、assetName 行の期待ダイジェストを返す。
func fetchExpectedSum(client *http.Client, url, assetName string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("SHA2-256SUMS 取得失敗: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("SHA2-256SUMS 取得失敗: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return parseSums(data, assetName)
}

// parseSums は SHA2-256SUMS の内容から assetName 行（"<hexdigest>  <filename>"）の
// 期待ダイジェストを返す。見つからなければエラー。
func parseSums(data []byte, assetName string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == assetName {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("SHA2-256SUMS に %s のエントリがありません", assetName)
}
