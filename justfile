
build:
    go build -ldflags "-s -w" -o bin/bootstrap ./
    touch --no-dereference --date='2001-01-01 00:00:00' bin/bootstrap

zip: build
    cd bin; zip bootstrap bootstrap

clean:
    rm -rf bin
