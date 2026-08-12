module companion-server

go 1.23.0

require (
 github.com/coder/websocket v1.8.15
 gopkg.in/hraban/opus.v2 v2.0.0-20230925203106-0188a62cb302
 modernc.org/sqlite v1.56.0
)
replace github.com/coder/websocket => ./offline_deps/coder_websocket
replace gopkg.in/hraban/opus.v2 => ./offline_deps/hraban_opus
replace modernc.org/sqlite => ./offline_deps/modernc_sqlite
