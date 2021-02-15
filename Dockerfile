FROM golang:1.15 as builder

WORKDIR /go/src/github.com/episub/gedoc/
COPY . .
RUN cd server && go build -o server

FROM episub/gedoc-base
RUN mkdir -p /gedoc/build
WORKDIR /gedoc
COPY --from=builder /go/src/github.com/episub/gedoc/server/server /server
COPY server/build/.latexmkrc /gedoc/build
COPY policy.xml /etc/ImageMagick-6/policy.xml
CMD ["/server"]
