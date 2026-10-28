FROM golang:1.23-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY web ./web
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/shockstock .

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/shockstock /shockstock

ENV LISTEN_ADDR=:8080
ENV DATA_DIR=/data

VOLUME ["/data"]
EXPOSE 8080

ENTRYPOINT ["/shockstock"]
