using System.Diagnostics;
using System.IO.Compression;
using System.Net.Http.Json;
using System.Security.Cryptography;
using System.Text.Json;

namespace JonaHomelab.Companion;

public sealed class UpdateCoordinator
{
    private const string Repository = "jonatancheca/jona-homelab";
    private const string AssetName = "jona-homelab-companion-win-x64.zip";
    private const string ChecksumName = AssetName + ".sha256";
    private readonly SemaphoreSlim lockObject = new(1, 1);
    private readonly HttpClient client = new() { Timeout = TimeSpan.FromSeconds(30) };

    public UpdateCoordinator()
    {
        client.DefaultRequestHeaders.UserAgent.ParseAdd("JonaHomelabCompanion/1");
    }

    public async Task<bool> CheckAndScheduleAsync(CancellationToken cancellationToken)
    {
        if (!await lockObject.WaitAsync(0, cancellationToken)) return false;
        try
        {
            using var response = await client.GetAsync($"https://api.github.com/repos/{Repository}/releases/latest", cancellationToken);
            response.EnsureSuccessStatusCode();
            using var release = await response.Content.ReadFromJsonAsync<JsonDocument>(cancellationToken: cancellationToken) ?? throw new InvalidOperationException("Invalid release response.");
            var tag = release.RootElement.GetProperty("tag_name").GetString() ?? string.Empty;
            if (tag == CompanionConfiguration.ReleaseVersion || !System.Text.RegularExpressions.Regex.IsMatch(tag, "^main-[0-9a-f]{12}$")) return false;
            var assets = release.RootElement.GetProperty("assets").EnumerateArray().ToDictionary(item => item.GetProperty("name").GetString() ?? string.Empty, item => item.GetProperty("browser_download_url").GetString() ?? string.Empty);
            if (!assets.TryGetValue(AssetName, out var archive) || !assets.TryGetValue(ChecksumName, out var checksum) || !IsGithubDownload(archive) || !IsGithubDownload(checksum)) return false;
            var startInfo = new ProcessStartInfo(Environment.ProcessPath!) { UseShellExecute = false, CreateNoWindow = true };
            startInfo.ArgumentList.Add("--update");
            startInfo.ArgumentList.Add(tag);
            startInfo.ArgumentList.Add(archive);
            startInfo.ArgumentList.Add(checksum);
            startInfo.ArgumentList.Add(Environment.ProcessId.ToString());
            var process = Process.Start(startInfo);
            return process is not null;
        }
        catch { return false; }
        finally { lockObject.Release(); }
    }

    public static async Task<int> RunUpdaterAsync(string[] args)
    {
        if (args.Length != 4) return 2;
        var tag = args[0];
        var archiveUrl = args[1];
        var checksumUrl = args[2];
        if (!RegexVersion(tag)) return 2;
        if (!IsGithubDownload(archiveUrl) || !IsGithubDownload(checksumUrl)) return 2;
        if (!int.TryParse(args[3], out var parentPid)) return 2;
        var root = InstallationRoot();
        if (string.IsNullOrWhiteSpace(root)) return 2;
        var current = Path.Combine(root, "current");
        var releases = Path.Combine(root, "releases");
        var oldTarget = ResolveTarget(current);
        if (oldTarget is null || !IsWithinDirectory(releases, oldTarget)) return 2;
        var staging = Path.Combine(CompanionConfiguration.DataDirectory, "update", tag);
        Directory.CreateDirectory(staging);
        var archive = Path.Combine(staging, AssetName);
        var checksum = Path.Combine(staging, ChecksumName);
        using var client = new HttpClient { Timeout = TimeSpan.FromMinutes(2) };
        client.DefaultRequestHeaders.UserAgent.ParseAdd("JonaHomelabCompanionUpdater/1");
        var serviceStopped = false;
        var switched = false;
        try
        {
            await File.WriteAllBytesAsync(archive, await client.GetByteArrayAsync(archiveUrl));
            var expected = (await client.GetStringAsync(checksumUrl)).Split((char[]?)null, StringSplitOptions.RemoveEmptyEntries).FirstOrDefault()?.ToLowerInvariant();
            using var archiveStream = File.OpenRead(archive);
            if (expected is null || !string.Equals(Convert.ToHexString(await SHA256.HashDataAsync(archiveStream)).ToLowerInvariant(), expected, StringComparison.Ordinal)) return 3;
            var extracted = Path.Combine(staging, "extracted");
            if (Directory.Exists(extracted)) Directory.Delete(extracted, true);
            ZipFile.ExtractToDirectory(archive, extracted);
            if (!File.Exists(Path.Combine(extracted, "JonaHomelab.Companion.exe")) || File.ReadAllText(Path.Combine(extracted, "RELEASE_VERSION")).Trim() != tag) return 4;
            Run("sc.exe", $"stop {CompanionConfiguration.ServiceName}");
            serviceStopped = true;
            await WaitForExit(parentPid);
            foreach (var process in Process.GetProcessesByName("JonaHomelab.Companion")) { try { if (process.Id != Environment.ProcessId) process.Kill(true); } catch { } }
            Directory.CreateDirectory(releases);
            var target = Path.Combine(releases, tag);
            if (Directory.Exists(target)) Directory.Delete(target, true);
            Directory.Move(extracted, target);
            ReplaceJunction(current, target);
            switched = true;
            Run("sc.exe", $"start {CompanionConfiguration.ServiceName}");
            if (await Healthy(tag)) { serviceStopped = false; TryRunTask(); return 0; }
            ReplaceJunction(current, oldTarget);
            switched = false;
            Run("sc.exe", $"start {CompanionConfiguration.ServiceName}");
            serviceStopped = false;
            return 5;
        }
        catch
        {
            try
            {
                if (serviceStopped)
                {
                    if (switched && Directory.Exists(oldTarget)) ReplaceJunction(current, oldTarget);
                    Run("sc.exe", $"start {CompanionConfiguration.ServiceName}");
                }
            }
            catch { }
            return 6;
        }
        finally { try { Directory.Delete(staging, true); } catch { } }
    }

    private static async Task WaitForExit(int pid)
    {
        try
        {
            using var process = Process.GetProcessById(pid);
            using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(30));
            await process.WaitForExitAsync(timeout.Token);
        }
        catch (OperationCanceledException) { throw new TimeoutException("The previous Companion process did not stop."); }
        catch (ArgumentException) { }
    }

    private static bool RegexVersion(string version) => System.Text.RegularExpressions.Regex.IsMatch(version, "^main-[0-9a-f]{12}$");
    private static bool IsGithubDownload(string url) => Uri.TryCreate(url, UriKind.Absolute, out var parsed) && parsed.Scheme == Uri.UriSchemeHttps && (parsed.Host.Equals("github.com", StringComparison.OrdinalIgnoreCase) || parsed.Host.Equals("objects.githubusercontent.com", StringComparison.OrdinalIgnoreCase));
    private static string? InstallationRoot()
    {
        var directory = new DirectoryInfo(AppContext.BaseDirectory.TrimEnd(Path.DirectorySeparatorChar));
        if (directory.Name.Equals("current", StringComparison.OrdinalIgnoreCase)) return directory.Parent?.FullName;
        if (directory.Parent?.Name.Equals("releases", StringComparison.OrdinalIgnoreCase) == true) return directory.Parent.Parent?.FullName;
        return directory.Parent?.FullName;
    }
    private static string? ResolveTarget(string path)
    {
        try { return new DirectoryInfo(path).ResolveLinkTarget(true)?.FullName; }
        catch { return null; }
    }

    private static bool IsWithinDirectory(string directory, string candidate)
    {
        var root = Path.GetFullPath(directory).TrimEnd(Path.DirectorySeparatorChar) + Path.DirectorySeparatorChar;
        var path = Path.GetFullPath(candidate).TrimEnd(Path.DirectorySeparatorChar) + Path.DirectorySeparatorChar;
        return path.StartsWith(root, StringComparison.OrdinalIgnoreCase);
    }

    private static void ReplaceJunction(string path, string target)
    {
        var existing = new DirectoryInfo(path);
        if (existing.Exists)
        {
            if (!existing.Attributes.HasFlag(FileAttributes.ReparsePoint)) throw new InvalidOperationException("Refusing to replace a non-link installation path.");
            existing.Delete(false);
        }
        if (!Directory.Exists(target) || !Run("cmd.exe", $"/c mklink /J \"{path}\" \"{target}\"")) throw new InvalidOperationException("Could not activate Companion release.");
    }

    private static bool Run(string file, string arguments)
    {
        using var process = Process.Start(new ProcessStartInfo(file, arguments) { UseShellExecute = false, CreateNoWindow = true });
        if (process is null || !process.WaitForExit(30000)) return false;
        return process.ExitCode == 0;
    }

    private static async Task<bool> Healthy(string expectedVersion)
    {
        using var client = new HttpClient { Timeout = TimeSpan.FromSeconds(2) };
        for (var attempt = 0; attempt < 30; attempt++)
        {
            try
            {
                using var document = JsonDocument.Parse(await client.GetStringAsync($"http://127.0.0.1:{CompanionConfiguration.Port}/health"));
                if (document.RootElement.TryGetProperty("status", out var status) && status.GetString() == "ok" && document.RootElement.TryGetProperty("version", out var version) && version.GetString() == expectedVersion) return true;
            }
            catch { }
            await Task.Delay(2000);
        }
        return false;
    }

    private static void TryRunTask() { try { Run("schtasks.exe", "/Run /TN JonaHomelabCompanionTray"); } catch { } }
}
