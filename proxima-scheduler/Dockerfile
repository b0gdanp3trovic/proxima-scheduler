FROM golang:1.22

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -o /pinger ./cmd/pinger
RUN go build -o /proxima-scheduler ./cmd/scheduler
RUN go build -o /edgeproxy ./cmd/edgeproxy
RUN go build -o /admissionhandler ./cmd/admissionhandler

# Default command, can be overridden
CMD ["/scheduler"]
