/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"slices"

	"os"

	"github.com/etcd-io/auger/pkg/data"
	"github.com/etcd-io/auger/pkg/encoding"
	"github.com/etcd-io/auger/pkg/scheme"
	"github.com/spf13/cobra"
)

type dockerOptions struct {
	*extractOptions
	outFile  string
	registry string
}

var dockerOpts = &dockerOptions{
	extractOptions: &extractOptions{},
}

// dockerCmd represents the docker command
var dockerCmd = &cobra.Command{
	Use:   "docker",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Run: func(cmd *cobra.Command, args []string) {
		docker(dockerOpts)
	},
}

func docker(o *dockerOptions) {
	var err error
	outMediaType, err := encoding.ToMediaType(opts.out)
	if err != nil {
		fmt.Printf("invalid --output %s: %v", opts.out, err)
		return
	}
	keys2Revsions, err := data.ListAllKeysVersions(o.filename, o.registry)
	if err != nil {
		return
	}
	outPutFile, err := os.OpenFile(o.outFile, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	outPutFile.WriteString(fmt.Sprintf("key,lastet validVersion,corruptVersions,HasCorrupt\n",)
	for key, arr := range keys2Revsions {
		SortRevisionsDesc(arr)
		corruptVersions := make([]int64, 0)
		validVersion := int64(0)
		for _, v := range arr {
			if isDataCorrupt(o.filename, key, v, outMediaType) {
				corruptVersions = append(corruptVersions, v)
			} else {
				validVersion = v
				break
			}
		}
		outPutFile.WriteString(fmt.Sprintf("%s,%d,%+v,%T\n", key, validVersion, corruptVersions, len(corruptVersions) == 0))
	}
	outPutFile.Close()
}

func SortRevisionsDesc(arr []int64) {
	slices.SortFunc(arr, func(i, j int64) int {
		if i <= j {
			return 1
		}
		return -1
	})
}

func isDataCorrupt(filename string, key string, v int64, outMediaType string) bool {
	in, err := data.GetValue(filename, key, v)
	if err != nil {
		return true
	}
	if len(in) == 0 {
		return true
	}

	_, _, err = encoding.DetectAndConvert(scheme.Codecs, outMediaType, in)
	if err != nil {
		return true
	}
	return false
}

func init() {
	RootCmd.AddCommand(dockerCmd)
	dockerCmd.Flags().StringVarP(&dockerOpts.outFile, "outFile", "", "/tmp/auger.docker.csv", "	Output file")
	dockerCmd.Flags().StringVarP(&dockerOpts.registry, "registry", "", "/registry", " prefix ")
	dockerCmd.Flags().StringVarP(&dockerOpts.filename, "file", "f", "", "Bolt DB '.db' filename")
}
