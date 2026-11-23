// Package native describes the Xray statistics RPC used by Node.
package native

//go:generate protoc -I . --go_out=. --go_opt=paths=source_relative stats.proto

const GetSysStats = "/xray.app.stats.command.StatsService/GetSysStats"
