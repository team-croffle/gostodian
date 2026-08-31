# syntax=docker/dockerfile:1
ARG GO_VERSION=1.26

# ---------------------------------------------------------------- build
FROM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-mod=readonly

COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
ARG VERSION=dev
ARG COMMIT=none
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -buildvcs=false \
      -ldflags="-s -w -buildid= -X main.version=${VERSION} -X main.commit=${COMMIT}" \
      -o /out/gostodian .

# ---------------------------------------------------------------- runtime
# x/crypto/ssh 라이브러리를 쓰면 ssh 바이너리가 필요 없으므로 셸 없는 static 이미지로 내려감.
# 컨테이너가 탈취돼도 실행할 셸/유틸이 존재하지 않음.
FROM gcr.io/distroless/static-debian12:nonroot
ENV TZ=Asia/Seoul
COPY --from=build /out/gostodian /usr/local/bin/gostodian
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/gostodian"]
