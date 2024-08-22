FROM golang:1.22

COPY go.mod ./
RUN go mod download

COPY *.go ./

RUN go build -o /proxima-scheduler
CMD ["/proxima-scheduler"]