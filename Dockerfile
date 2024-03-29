FROM golang:1.16 as builder

WORKDIR /go/src/github.com/episub/gedoc/
COPY go.mod go.mod
COPY go.sum go.sum
COPY server server
COPY gedoc gedoc
RUN cd server && go build -o server

FROM episub/gedoc-base:test
RUN mkdir /gedoc
WORKDIR /gedoc
COPY --from=builder /go/src/github.com/episub/gedoc/server/server /server
COPY server/blank.pdf /gedoc
COPY policy.xml /etc/ImageMagick-6/policy.xml
HEALTHCHECK --timeout=3s \
    CMD curl -f http://localhost:50052/health || exit 1
CMD ["/server"]

EXPOSE 50051
