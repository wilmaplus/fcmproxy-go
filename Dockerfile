FROM golang:1.21.5

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./

RUN CGO_ENABLED=0 GOOS=linux go build -o /fcmproxy

CMD ["/fcmproxy", "--backward=true"]