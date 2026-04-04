module agentic9

go 1.26

require (
	github.com/BurntSushi/toml v1.5.0
	github.com/hanwen/go-fuse/v2 v2.7.2
)

require (
	github.com/jc-lab/go-tls-psk v1.18.3-r002 // indirect
	golang.org/x/crypto v0.43.0 // indirect
	golang.org/x/sys v0.37.0 // indirect
)

replace github.com/jc-lab/go-tls-psk => ./third_party/go-tls-psk
