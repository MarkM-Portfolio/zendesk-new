dev: main.go zd_custom_types.go
	GOOS=linux GOARCH=amd64 go build -o dev.amd64 $(GOFLAGS) main.go zd_custom_types.go

run: dev
	sam local start-api --template dev.yaml --host `ipconfig getifaddr en0` --port 5080

release: clean main.go zd_custom_types.go
	GOOS=linux GOARCH=arm64 go build -o bootstrap $(GOFLAGS) main.go zd_custom_types.go

clean:
	rm -f dev.amd64 bootstrap *.zip
