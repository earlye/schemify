module github.com/earlye/schemify/go

go 1.26.1

require (
	github.com/earlye/eaux/go/types v0.0.1
	github.com/earlye/schemify/go/schemify v0.0.0
	github.com/go-errors/errors v1.5.1
	github.com/spf13/cobra v1.10.2
)

replace github.com/earlye/schemify/go/schemify => ./schemify

require (
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/earlye/postgresparser v0.0.3 // indirect
	github.com/earlye/sensitive-strings/golang/ss v0.0.2 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.8.0 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	golang.org/x/exp v0.0.0-20240506185415-9bf2ced13842 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/text v0.29.0 // indirect
)
