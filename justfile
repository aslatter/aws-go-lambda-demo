
build:
    go build -ldflags "-s -w" -o bin/bootstrap ./
    touch --no-dereference --date='2001-01-01 00:00:00' bin/bootstrap

zip: build
    @cd bin; zip bootstrap bootstrap
    @printf "bootstrap:\t%s\n" "$(<bin/bootstrap sha256sum --binary | xxd -r -p | base64)"
    @printf "bootstrap.zip:\t%s\n" "$(<bin/bootstrap.zip sha256sum --binary | xxd -r -p | base64)"

clean:
    rm -rf bin
