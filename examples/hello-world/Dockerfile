FROM golang:1.25-alpine

WORKDIR /app

COPY examples/hello-world/go.mod examples/hello-world/go.sum* ./
RUN go mod download

COPY examples/hello-world/main.go .

RUN go build -o server main.go

EXPOSE 8080

CMD ["./server"]
