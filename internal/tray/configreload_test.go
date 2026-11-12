package tray

import (
	"fmt"
	"testing"
)

func TestNotifyQueueDropsOldestWhenFull(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	posted := make(chan string, 100)
	q := &notifyQueue{post: func(title, _ string) {
		select {
		case started <- struct{}{}:
			<-release
		default:
		}
		posted <- title
	}}

	q.push(notification{title: "n0"})
	<-started

	const total = notifyQueueSize + 5
	for i := 1; i <= total; i++ {
		q.push(notification{title: fmt.Sprintf("n%d", i)})
	}
	close(release)

	want := []string{"n0"}
	for i := total - notifyQueueSize + 1; i <= total; i++ {
		want = append(want, fmt.Sprintf("n%d", i))
	}
	for _, w := range want {
		if got := <-posted; got != w {
			t.Fatalf("posted %q, want %q", got, w)
		}
	}
}
