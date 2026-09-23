// workdir.go: ダウンロードごとの作業ディレクトリの記録と、起動時の残骸掃除。

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func workDirRegistryPath() (string, error) {
	dir, err := ytDlpDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "workdirs.json"), nil
}

func readWorkDirRegistry() []string {
	p, err := workDirRegistryPath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var dirs []string
	json.Unmarshal(data, &dirs) //nolint:errcheck
	return dirs
}

func writeWorkDirRegistry(dirs []string) {
	p, err := workDirRegistryPath()
	if err != nil {
		return
	}
	data, err := json.Marshal(dirs)
	if err != nil {
		return
	}
	os.WriteFile(p, data, 0o644) //nolint:errcheck
}

func registerWorkDir(path string) {
	dirs := readWorkDirRegistry()
	dirs = append(dirs, path)
	writeWorkDirRegistry(dirs)
}

func unregisterWorkDir(path string) {
	dirs := readWorkDirRegistry()
	filtered := dirs[:0]
	for _, d := range dirs {
		if d != path {
			filtered = append(filtered, d)
		}
	}
	writeWorkDirRegistry(filtered)
}

// workDirPrefix は runDownload が outputDir 内に作る一時作業ディレクトリ名の
// プレフィックス（os.MkdirTemp のパターン）。cleanupLeftoverWorkDirs の削除ガードにも使う。
const workDirPrefix = ".moviedl-work-"

// isManagedWorkDir は path がこのアプリの作業ディレクトリ（basename が
// workDirPrefix 始まり）かを判定する。cleanupLeftoverWorkDirs はこのガードを
// 通過したパスだけを os.RemoveAll するため、workdirs.json が改竄・破損して
// 不正なパスが混入しても任意ディレクトリを削除しない。
// aidlc-docs/inception/application-design/design.md「workDir 削除はプレフィックス検証必須」参照。
func isManagedWorkDir(path string) bool {
	return strings.HasPrefix(filepath.Base(path), workDirPrefix)
}

func cleanupLeftoverWorkDirs() {
	dirs := readWorkDirRegistry()
	for _, d := range dirs {
		if isManagedWorkDir(d) {
			os.RemoveAll(d) //nolint:errcheck
		}
	}
	writeWorkDirRegistry(nil)
}
