FROM golang:1.27.2-bookworm

ENV PLAYWRIGHT_DRIVER_PATH=/opt/playwright-driver \
    HOME=/tmp

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
COPY tests/*.go ./tests/
RUN go build -o /usr/local/bin/playwright github.com/mxschmitt/playwright-go/cmd/playwright && \
    playwright --version && \
    chmod 0755 /opt/playwright-driver/node && \
    go test -c -race -o /usr/local/bin/camoufox-unit-tests . && \
    go test -c -race -tags=integration -o /usr/local/bin/camoufox-integration-tests ./tests

USER 1000:1000

CMD ["sh", "-c", "/usr/local/bin/camoufox-unit-tests -test.v -test.count=1 -test.failfast -test.timeout=1m && exec /usr/local/bin/camoufox-integration-tests -test.v -test.count=1 -test.failfast -test.timeout=3m"]
