package dnstunnel

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/Dreamacro/clash/log"
)

func trimLastDot(s string) string {
	if len(s) > 0 && s[len(s)-1] == '.' {
		return s[:len(s)-1]
	}
	return s
}

func readBytesFromFile(filePath string) ([]byte, error) {
	return os.ReadFile(filePath)
}

func writeFileEnsureDir(filePath string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return fmt.Errorf("create directory failed: %v", err)
	}

	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("create file failed: %v", err)
	}
	defer file.Close()

	_, err = file.Write(data)
	return err
}

func getDirSize(dirPath string) (int64, error) {
	var size int64
	err := filepath.Walk(dirPath, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}

func removeOldestFiles(dirPath string) error {
	files, err := os.ReadDir(dirPath)
	if err != nil {
		return err
	}

	sort.Slice(files, func(i, j int) bool {
		info, _ := files[i].Info()
		info2, _ := files[j].Info()
		return info.ModTime().Before(info2.ModTime())
	})

	for i := 0; i < len(files)/2; i++ {
		if err := os.Remove(filepath.Join(dirPath, files[i].Name())); err != nil {
			log.Errorln("remove file %s failed: %v", files[i].Name(), err)
		}
	}
	return nil
}

func deleteDirSize(dirPath string, limit int64) {
	size, err := getDirSize(dirPath)
	if err != nil {
		log.Errorln("get directory size failed: %v", err)
		return
	}

	if size > limit {
		if err := removeOldestFiles(dirPath); err != nil {
			log.Errorln("remove oldest files failed: %v", err)
		}
	}
}

func RemoveDuplicates[T any, K comparable](slice []T, keyFunc func(T) K) []T {
	seen := make(map[K]bool)
	var result []T
	for _, item := range slice {
		key := keyFunc(item)
		if !seen[key] {
			seen[key] = true
			result = append(result, item)
		}
	}
	return result
}
