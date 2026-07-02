package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Build metadata. Override at build time via -ldflags.
//
//	go build -ldflags "-X main.version=v0.2.0 -X main.commit=$(git rev-parse --short HEAD)"
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// herculesPBSchemaVersion is the Hercules protobuf Metadata.version this build expects.
// Bump when pb.proto is resynced from ../hercules/internal/pb/pb.proto.
const herculesPBSchemaVersion = 2

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version, build, and Hercules schema compatibility information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("labours-go %s (commit %s, built %s, %s/%s)\n",
			version, commit, date, runtime.GOOS, runtime.GOARCH)
		fmt.Printf("hercules protobuf schema: v%d\n", herculesPBSchemaVersion)
		fmt.Printf("go: %s\n", runtime.Version())
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
	rootCmd.PersistentFlags().BoolP("version", "V", false, "Print version information and exit")
	// root.go's init binds flags before this file's init runs, so re-bind
	// the new flag explicitly here.
	_ = viper.BindPFlag("version", rootCmd.PersistentFlags().Lookup("version"))
}
