package pgstore

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"companion-server/internal/idempotency"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func postgresTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("COMPANION_POSTGRES_TEST_DSN")
	if dsn == "" { t.Skip("COMPANION_POSTGRES_TEST_DSN not set") }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil { t.Fatal(err) }
	if err := pool.Ping(ctx); err != nil { pool.Close(); t.Fatal(err) }
	t.Cleanup(pool.Close)
	return pool
}

func TestPostgresIdempotencyReplayConflictAndActorIsolation(t *testing.T) {
	pool := postgresTestPool(t)
	ctx := context.Background()
	hashA, _ := idempotency.HashValue(map[string]any{"content":"hello"})
	hashB, _ := idempotency.HashValue(map[string]any{"content":"different"})
	prefix := "pg-phase-b-replay"
	_, _ = pool.Exec(ctx, `DELETE FROM idempotency_records WHERE idempotency_key LIKE $1`, prefix+"%")
	_, _ = pool.Exec(ctx, `DELETE FROM notes WHERE user_id LIKE $1`, prefix+"%")

	request := idempotency.Request{Actor:prefix+"-actor",Operation:"note.create",Key:prefix+"-key",RequestHash:hashA}
	var calls atomic.Int32
	mutate := func(tx pgx.Tx) (any,error) {
		calls.Add(1)
		var id int64
		err := tx.QueryRow(ctx, `INSERT INTO notes(idempotency_key,user_id,content,created_at) VALUES($1,$2,$3,$4) RETURNING id`, request.Key, prefix+"-user", "hello", time.Now().UTC()).Scan(&id)
		return map[string]any{"id":id,"content":"hello"}, err
	}
	first, err := RunIdempotent(ctx,pool,request,mutate); if err != nil { t.Fatal(err) }
	second, err := RunIdempotent(ctx,pool,request,func(pgx.Tx)(any,error){ t.Fatal("replay executed mutation"); return nil,nil }); if err != nil { t.Fatal(err) }
	if first.Replayed || !second.Replayed || first.JSON != second.JSON || calls.Load()!=1 { t.Fatalf("first=%+v second=%+v calls=%d",first,second,calls.Load()) }
	var decoded map[string]any; if err:=json.Unmarshal([]byte(first.JSON),&decoded);err!=nil{t.Fatal(err)}

	conflictReq := request; conflictReq.RequestHash = hashB
	if _,err:=RunIdempotent(ctx,pool,conflictReq,func(pgx.Tx)(any,error){t.Fatal("conflict executed mutation");return nil,nil}); !idempotency.IsConflict(err) { t.Fatalf("conflict error=%v",err) }

	actor2 := request; actor2.Actor = prefix+"-actor-2"
	if _,err:=RunIdempotent(ctx,pool,actor2,func(tx pgx.Tx)(any,error){return map[string]any{"actor":2},nil});err!=nil{t.Fatalf("actor isolation: %v",err)}
}

func TestPostgresIdempotencyConcurrentFirstAttemptMutatesOnce(t *testing.T) {
	pool := postgresTestPool(t)
	ctx := context.Background()
	hash, _ := idempotency.HashValue(map[string]any{"amount":50000})
	prefix := "pg-phase-b-concurrent"
	_, _ = pool.Exec(ctx, `DELETE FROM idempotency_records WHERE actor_id=$1`, prefix)
	_, _ = pool.Exec(ctx, `DELETE FROM notes WHERE user_id=$1`, prefix)
	request := idempotency.Request{Actor:prefix,Operation:"note.create",Key:"same",RequestHash:hash}
	var mutations atomic.Int32
	const workers = 8
	results := make(chan IdempotentOutcome, workers)
	errs := make(chan error, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i:=0;i<workers;i++{
		wg.Add(1)
		go func(){
			defer wg.Done(); <-start
			out,err:=RunIdempotent(ctx,pool,request,func(tx pgx.Tx)(any,error){
				mutations.Add(1)
				var id int64
				err:=tx.QueryRow(ctx,`INSERT INTO notes(idempotency_key,user_id,content,created_at) VALUES($1,$2,$3,$4) RETURNING id`,request.Key,prefix,"once",time.Now().UTC()).Scan(&id)
				return map[string]any{"id":id},err
			})
			if err!=nil{errs<-err;return};results<-out
		}()
	}
	close(start);wg.Wait();close(results);close(errs)
	for err:=range errs{t.Fatalf("concurrent run: %v",err)}
	if mutations.Load()!=1{t.Fatalf("mutation count=%d want=1",mutations.Load())}
	var baseline string; replayed:=0
	for out:=range results{if baseline==""{baseline=out.JSON};if out.JSON!=baseline{t.Fatalf("outcome mismatch: %q != %q",out.JSON,baseline)};if out.Replayed{replayed++}}
	if replayed!=workers-1{t.Fatalf("replayed=%d want=%d",replayed,workers-1)}
	var rows int; if err:=pool.QueryRow(ctx,`SELECT count(*) FROM notes WHERE user_id=$1`,prefix).Scan(&rows);err!=nil{t.Fatal(err)};if rows!=1{t.Fatalf("note rows=%d want=1",rows)}
}
