#!/usr/bin/env bash

mkdir -p /usr/src/gin-persist-log/coverage
/usr/local/go/bin/go test -C /usr/src/gin-persist-log -coverprofile "/usr/src/gin-persist-log/coverage/cover_$(date '+%Y%m%d%H%M%S%N').out" ./...
