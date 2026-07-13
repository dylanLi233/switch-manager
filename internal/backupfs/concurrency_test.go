package backupfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sync"
	"testing"

	"github.com/dylanLi233/switch-manager/internal/apperror"
)

func TestConcurrentPutSamePathCommitsExactlyOneFile(t *testing.T) {
	storage, _ := newTestStorage(t, 1024)
	contents := [][]byte{[]byte("first-complete-backup"), []byte("second-complete-backup")}
	type outcome struct {
		index int
		err   error
	}
	start := make(chan struct{})
	results := make(chan outcome, len(contents))
	var workers sync.WaitGroup
	for index := range contents {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			_, err := storage.Put(context.Background(), "device/shared.cfg", bytes.NewReader(contents[index]))
			results <- outcome{index: index, err: err}
		}(index)
	}
	close(start)
	workers.Wait()
	close(results)

	successes := 0
	winner := -1
	conflicts := 0
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			winner = result.index
		case apperror.IsCode(result.err, apperror.CodeStateConflict):
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", result.err)
		}
	}
	if successes != 1 || conflicts != 1 || winner < 0 {
		t.Fatalf("successes=%d conflicts=%d winner=%d", successes, conflicts, winner)
	}
	artifactSHA := sha256Hex(contents[winner])
	file, err := storage.OpenVerified(context.Background(), "device/shared.cfg", artifactSHA, int64(len(contents[winner])))
	if err != nil {
		t.Fatal(err)
	}
	actual, err := io.ReadAll(file)
	_ = file.Close()
	if err != nil || !bytes.Equal(actual, contents[winner]) {
		t.Fatalf("actual=%q err=%v", actual, err)
	}
}

func sha256Hex(value []byte) string {
	hash := sha256.Sum256(value)
	return hex.EncodeToString(hash[:])
}
