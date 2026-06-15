package daemon

import "wepapered/internal/core"

// Shared state lives in internal/core so the gui/settings/ctl binaries can use it
// without linking the LWE library this package pulls in. These aliases let the
// daemon's files keep their original unqualified names (Config, loadConfig, …).
type Config = core.Config

var (
	loadConfig       = core.LoadConfig
	saveConfig       = core.SaveConfig
	resolveWEPath    = core.ResolveWEPath
	weDirValid       = core.WeDirValid
	steamLibraryDirs = core.SteamLibraryDirs

	lweOutputDir     = core.LWEOutputDir
	lwebin           = core.LWEBin
	lwesubprocessbin = core.LWESubprocessBin

	siblingBinary = core.SiblingBinary

	acquireControlSocket = core.AcquireControlSocket
	listenRandomPort     = core.ListenRandomPort
	controlSocketPath    = core.ControlSocketPath

	buildVersion = core.Version

	sessionUID        = core.SessionUID
	sessionUsername   = core.SessionUsername
	waylandSessionEnv = core.WaylandSessionEnv
)

const settingsBinary = core.SettingsBinary
