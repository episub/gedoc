FROM golang:1.11

WORKDIR /go/src/github.com/episub/gedoc/
COPY . .
RUN GO111MODULE=on go mod vendor
RUN go build server/*go

FROM episub/gedoc-base
RUN mkdir -p /gedoc/build
WORKDIR /gedoc
COPY --from=0 /go/src/github.com/episub/gedoc/main .
COPY build/.latexmkrc /gedoc/build
CMD ["./main"]
