package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"companion-server/internal/observability"

	"github.com/coder/websocket"
)

func TestWriteLoopTerminatesWithinSessionDeadlineAfterPeerTransportFailure(t *testing.T) {
	accepted := make(chan *websocket.Conn, 1)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		accepted <- connection
		<-r.Context().Done()
	}))
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	client, _, err := websocket.Dial(ctx, "ws"+httpServer.URL[len("http"):], nil)
	if err != nil {
		t.Fatal(err)
	}
	serverConnection := <-accepted
	defer serverConnection.CloseNow()

	s := &session{
		id: "writer-failure", connection: serverConnection,
		controlWrites: make(chan outbound, 1), mediaWrites: make(chan outbound, 1),
		observer: observability.Nop(),
	}
	done := make(chan error, 1)
	go func() { done <- s.writeLoop(ctx) }()

	client.CloseNow()
	time.Sleep(10 * time.Millisecond)
	s.controlWrites <- outbound{kind: websocket.MessageText, data: []byte(`{"type":"probe"}`)}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("writer loop returned nil after peer transport failure/session deadline")
		}
	case <-time.After(time.Second):
		t.Fatal("writer loop exceeded the bounded session failure window")
	}
}
