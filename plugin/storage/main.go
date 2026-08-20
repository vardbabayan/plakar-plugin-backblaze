package main

import (
	"os"

	sdk "github.com/PlakarKorp/go-kloset-sdk"
	"github.com/vardbabayan/plakar-plugin-backblaze/storage"
)

func main() {
	sdk.EntrypointStorage(os.Args, storage.NewStore)
}
