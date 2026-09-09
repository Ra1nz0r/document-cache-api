package main

import (
	"document-cache-api/internal/server"

	"github.com/rs/zerolog/log"
)

func main() {
	if err := server.Run(); err != nil {
		log.Fatal().Err(err).Msg("application stopped")
	}
}
