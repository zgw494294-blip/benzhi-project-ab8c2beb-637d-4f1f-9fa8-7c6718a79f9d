package artifactcachealias_test

import (
	"os"
	"path/filepath"
	"testing"

	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/store"
)

func TestArtifactCacheLoadIsolation(t *testing.T) {
	root := t.TempDir()
	writer, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"manifest":"original"}`
	input := []byte(want)
	if err := writer.SaveArtifact("arc-cache", "manifest-cache", input); err != nil {
		t.Fatal(err)
	}
	input[2] = 'X'
	fromSaveCache, err := writer.LoadArtifact("arc-cache", "manifest-cache")
	if err != nil {
		t.Fatal(err)
	}
	afterInputMutation := string(fromSaveCache)
	fromSaveCache[3] = 'Y'
	fromSaveCacheAgain, err := writer.LoadArtifact("arc-cache", "manifest-cache")
	if err != nil {
		t.Fatal(err)
	}

	reader, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	first, err := reader.LoadArtifact("arc-cache", "manifest-cache")
	if err != nil {
		t.Fatal(err)
	}
	first[4] = 'Z'

	disk, err := os.ReadFile(filepath.Join(root, "manifests", "arc-cache--manifest-cache.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(disk) != want {
		t.Fatalf("测试前提失效：磁盘制品被修改：%q", disk)
	}
	second, err := reader.LoadArtifact("arc-cache", "manifest-cache")
	if err != nil {
		t.Fatal(err)
	}
	if afterInputMutation != want || string(fromSaveCacheAgain) != want || string(second) != want {
		t.Fatalf("缓存所有权泄漏：save-hit %q repeated-hit %q disk-hit %q want %q", afterInputMutation, fromSaveCacheAgain, second, want)
	}
}
