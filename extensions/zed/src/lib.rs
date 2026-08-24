//! deja as a Zed context server.
//!
//! deja indexes the session files that other coding agents on the same machine
//! already wrote — Claude Code, Codex, Cursor, opencode and more — and answers
//! from them over MCP. This extension starts `deja mcp` for Zed's agent panel.
//!
//! A context server cannot look at $PATH — the extension API hands it a
//! project, and only a worktree can answer `which`. So the binary comes from
//! the `binary` setting when one is given, and otherwise from the project's
//! own GitHub releases, downloaded into the extension's directory.

use schemars::JsonSchema;
use serde::Deserialize;
use zed::settings::ContextServerSettings;
use zed_extension_api::{
    self as zed, Architecture, Command, ContextServerConfiguration, ContextServerId,
    DownloadedFileType, GithubReleaseOptions, Os, Project, Result, SlashCommand,
    SlashCommandOutput, SlashCommandOutputSection, Worktree,
};

const REPOSITORY: &str = "vshulcz/deja-vu";
const BINARY: &str = "deja";

#[derive(Debug, Default, Deserialize, JsonSchema)]
struct DejaSettings {
    /// Path to an installed deja binary. Leave it unset and the extension
    /// downloads a release build of its own.
    binary: Option<String>,
}

struct DejaExtension {
    cached_binary_path: Option<String>,
}

impl DejaExtension {
    /// The binary Zed should run: the configured one if the user named it,
    /// then the copy this extension downloaded earlier, and only then a fresh
    /// download of the current release.
    fn binary_path(&mut self, project: &Project) -> Result<String> {
        let settings = ContextServerSettings::for_project("deja-context-server", project)
            .ok()
            .and_then(|settings| settings.settings)
            .and_then(|value| zed::serde_json::from_value::<DejaSettings>(value).ok())
            .unwrap_or_default();

        if let Some(path) = settings.binary {
            return Ok(path);
        }

        if let Some(path) = self.cached_binary_path.clone() {
            return Ok(path);
        }

        // An installed deja is the one the user keeps current with `deja
        // update` or their package manager, so it wins over anything this
        // extension downloaded. A context server cannot ask the worktree for
        // $PATH, hence the well-known locations.
        if let Some(path) = installed_binary() {
            self.cached_binary_path = Some(path.clone());
            return Ok(path);
        }

        // A copy from an earlier session is worth more than a fresh one: asking
        // GitHub for the latest release is a network round trip, and it happens
        // inside the window Zed gives the server to answer `initialize`. Sixty
        // seconds is generous for a handshake and thin for a download.
        //
        // Left alone, though, that copy is the one this user runs forever. So
        // once a week the check is allowed to happen, and every failure inside
        // it falls back to what is already on disk: a stale deja answers, an
        // unreachable GitHub does not.
        if let Some(path) = downloaded_binary() {
            if !due_for_check() {
                self.cached_binary_path = Some(path.clone());
                return Ok(path);
            }
            record_check();
            match self.download_release() {
                Ok(fresh) => {
                    self.cached_binary_path = Some(fresh.clone());
                    return Ok(fresh);
                }
                Err(_) => {
                    self.cached_binary_path = Some(path.clone());
                    return Ok(path);
                }
            }
        }

        self.download_release()
    }

    /// Fetch the current release and unpack it beside the older ones.
    fn download_release(&mut self) -> Result<String> {

        // This is the one place an offline or firewalled machine gives up, so
        // the message has to say what to do rather than repeat the transport
        // error.
        let release = zed::latest_github_release(
            REPOSITORY,
            GithubReleaseOptions {
                require_assets: true,
                pre_release: false,
            },
        )
        .map_err(|error| {
            format!(
                "no deja binary was found and the release could not be fetched ({error}). \
                 Install deja — curl -fsSL \
                 https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh, or \
                 brew install deja-vu — and it will be used from there"
            )
        })?;

        let (os, arch) = zed::current_platform();
        // Release archives are named deja-vu_<version>_<os>_<arch>, with the
        // version carrying no leading v.
        let version = release.version.trim_start_matches('v');
        let (platform, extension, file_type) = match (os, arch) {
            (Os::Mac, Architecture::Aarch64) => ("darwin_arm64", "tar.gz", DownloadedFileType::GzipTar),
            (Os::Mac, Architecture::X8664) => ("darwin_amd64", "tar.gz", DownloadedFileType::GzipTar),
            (Os::Linux, Architecture::Aarch64) => ("linux_arm64", "tar.gz", DownloadedFileType::GzipTar),
            (Os::Linux, Architecture::X8664) => ("linux_amd64", "tar.gz", DownloadedFileType::GzipTar),
            (Os::Windows, Architecture::Aarch64) => ("windows_arm64", "zip", DownloadedFileType::Zip),
            (Os::Windows, Architecture::X8664) => ("windows_amd64", "zip", DownloadedFileType::Zip),
            _ => {
                return Err(format!(
                    "deja publishes no release build for this platform, so it has to be built \
                     from source: go install github.com/vshulcz/deja-vu/cmd/deja@latest, then \
                     set the \"binary\" setting to the path it lands in"
                ));
            }
        };

        let asset_name = format!("deja-vu_{version}_{platform}.{extension}");
        let asset = release
            .assets
            .into_iter()
            .find(|asset| asset.name == asset_name)
            .ok_or_else(|| format!("no asset named {asset_name} in the deja-vu release"))?;

        let version_dir = format!("deja-{version}");
        let binary_path = if os == Os::Windows {
            format!("{version_dir}/{BINARY}.exe")
        } else {
            format!("{version_dir}/{BINARY}")
        };

        zed::download_file(&asset.download_url, &version_dir, file_type)
            .map_err(|error| format!("downloading {asset_name} failed: {error}"))?;
        zed::make_file_executable(&binary_path)
            .map_err(|error| format!("{binary_path} is not executable: {error}"))?;

        self.cached_binary_path = Some(binary_path.clone());
        Ok(binary_path)
    }
}

const CHECK_MARKER: &str = ".last-release-check";
const CHECK_EVERY: u64 = 60 * 60 * 24 * 7;

/// Whether a week has passed since the last time the release was looked up.
/// An unreadable or unwritable marker means yes: a check that costs one request
/// is cheaper than a user pinned to an old binary forever.
fn due_for_check() -> bool {
    let Ok(text) = std::fs::read_to_string(CHECK_MARKER) else {
        return true;
    };
    let Ok(then) = text.trim().parse::<u64>() else {
        return true;
    };
    now_seconds().is_none_or(|now| now.saturating_sub(then) >= CHECK_EVERY)
}

fn record_check() {
    if let Some(now) = now_seconds() {
        let _ = std::fs::write(CHECK_MARKER, now.to_string());
    }
}

fn now_seconds() -> Option<u64> {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .ok()
        .map(|since| since.as_secs())
}

/// deja as installed by the user, if it sits in one of the places the install
/// script, Homebrew and `go install` put it.
fn installed_binary() -> Option<String> {
    if zed::current_platform().0 == Os::Windows {
        return None;
    }
    let home = std::env::var("HOME").ok()?;
    let candidates = [
        format!("{home}/.local/bin/deja"),
        "/opt/homebrew/bin/deja".to_string(),
        "/usr/local/bin/deja".to_string(),
        format!("{home}/go/bin/deja"),
    ];
    candidates
        .into_iter()
        .find(|path| std::fs::metadata(path).is_ok())
}

/// The newest `deja-<version>/deja` this extension downloaded before, if any.
fn downloaded_binary() -> Option<String> {
    let name = if zed::current_platform().0 == Os::Windows {
        "deja.exe"
    } else {
        "deja"
    };
    let mut found: Vec<String> = std::fs::read_dir(".")
        .ok()?
        .flatten()
        .filter_map(|entry| {
            let dir = entry.file_name().to_string_lossy().into_owned();
            if !dir.starts_with("deja-") {
                return None;
            }
            let candidate = format!("{dir}/{name}");
            std::fs::metadata(&candidate).ok().map(|_| candidate)
        })
        .collect();
    found.sort();
    found.pop()
}

impl zed::Extension for DejaExtension {
    fn new() -> Self {
        Self {
            cached_binary_path: None,
        }
    }

    fn context_server_command(
        &mut self,
        _context_server_id: &ContextServerId,
        project: &Project,
    ) -> Result<Command> {
        Ok(Command {
            command: self.binary_path(project)?,
            args: vec!["mcp".into()],
            env: vec![],
        })
    }

    /// `/deja <query>` — the same search the terminal gives, dropped into the
    /// thread. Unlike the context server this runs in the extension itself, so
    /// it can ask the worktree where `deja` is before falling back to the copy
    /// downloaded for the server.
    fn run_slash_command(
        &self,
        _command: SlashCommand,
        args: Vec<String>,
        worktree: Option<&Worktree>,
    ) -> Result<SlashCommandOutput> {
        let query = args.join(" ");
        if query.trim().is_empty() {
            return Err("say what to look for: /deja <error, file or decision>".into());
        }

        let binary = worktree
            .and_then(|worktree| worktree.which(BINARY))
            .or_else(downloaded_binary)
            .ok_or_else(|| {
                "deja is not on PATH and this extension has not downloaded a copy yet. Open the \
                 agent panel once so the context server fetches one, or install deja: curl -fsSL \
                 https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh"
                    .to_string()
            })?;

        let output = zed::process::Command::new(binary)
            .arg(query.clone())
            .envs(worktree.map(|worktree| worktree.shell_env()).unwrap_or_default())
            .output()?;

        let text = String::from_utf8_lossy(&output.stdout).trim().to_string();
        let text = if text.is_empty() {
            "Nothing in this machine's history matches that.".to_string()
        } else {
            text
        };

        Ok(SlashCommandOutput {
            sections: vec![SlashCommandOutputSection {
                range: (0..text.len() as u32).into(),
                label: format!("deja: {query}"),
            }],
            text,
        })
    }

    fn context_server_configuration(
        &mut self,
        _context_server_id: &ContextServerId,
        _project: &Project,
    ) -> Result<Option<ContextServerConfiguration>> {
        let installation_instructions =
            include_str!("../configuration/installation_instructions.md").to_string();
        let settings_schema = zed::serde_json::to_string(&schemars::schema_for!(DejaSettings))
            .map_err(|e| e.to_string())?;

        Ok(Some(ContextServerConfiguration {
            installation_instructions,
            default_settings: "{}".into(),
            settings_schema,
        }))
    }
}

zed::register_extension!(DejaExtension);
