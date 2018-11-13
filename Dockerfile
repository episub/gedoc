FROM golang:1.11 as builder

WORKDIR /go/src/github.com/episub/gedoc/
COPY . .
RUN GO111MODULE=on go mod vendor
RUN go build server/*go

FROM episub/gedoc-base
RUN mkdir -p /gedoc/build
WORKDIR /gedoc
COPY --from=builder /go/src/github.com/episub/gedoc/main .
COPY server/build/.latexmkrc /gedoc/build
CMD ["./main"]
