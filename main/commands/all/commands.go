package all

import (
	"github.com/drovosek229/Xray-core/main/commands/all/api"
	"github.com/drovosek229/Xray-core/main/commands/all/convert"
	"github.com/drovosek229/Xray-core/main/commands/all/tls"
	"github.com/drovosek229/Xray-core/main/commands/base"
)

func init() {
	base.RootCommand.Commands = append(
		base.RootCommand.Commands,
		api.CmdAPI,
		convert.CmdConvert,
		tls.CmdTLS,
		cmdUUID,
		cmdX25519,
		cmdWG,
		cmdMLDSA65,
		cmdMLKEM768,
		cmdVLESSEnc,
		cmdBuildMphCache,
	)
}
