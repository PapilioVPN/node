// Package scopeapi defines the private protocol between Node and its Xray child.
package scopeapi

//go:generate protoc -I . --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative scope.proto
