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

func TestWriteLoopTerminatesPromptlyAfterPeerTransportFailure(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
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
	// Let the peer FIN/RST become visible before forcing a server write. The
	// test does not depend on readLoop: it exercises the actual WebSocket writer.
	time.Sleep(10 * time.Millisecond)
	s.controlWrites <- outbound{kind: websocket.MessageText, data: []byte(`{"type":"probe"}`)}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("writer loop returned nil after peer transport failure")
		}
	case <-time.After(time.Second):
		t.Fatal("writer loop did not terminate within the bounded failure window")
	}
}
