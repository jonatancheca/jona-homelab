using System.Diagnostics;

namespace JonaHomelab.Companion;

public sealed class ShutdownExecutor
{
    private const int CooldownSeconds = 10;
    private long lastAttemptTicks;
    private readonly Action<bool> execute;

    public ShutdownExecutor(Action<bool>? execute = null) { this.execute = execute ?? Execute; }

    public bool TrySchedule(bool force)
    {
        var now = DateTime.UtcNow.Ticks;
        var previous = Interlocked.Read(ref lastAttemptTicks);
        if (previous != 0 && new TimeSpan(now - previous).TotalSeconds < CooldownSeconds) return false;
        if (Interlocked.CompareExchange(ref lastAttemptTicks, now, previous) != previous) return false;

        _ = Task.Run(async () =>
        {
            await Task.Delay(300).ConfigureAwait(false);
            execute(force);
        });
        return true;
    }

    private static void Execute(bool force)
    {
        var arguments = force ? "/s /t 0 /f" : "/s /t 0";
        using var process = Process.Start(new ProcessStartInfo
        {
            FileName = Path.Combine(Environment.SystemDirectory, "shutdown.exe"),
            Arguments = arguments,
            UseShellExecute = false,
            CreateNoWindow = true,
            WindowStyle = ProcessWindowStyle.Hidden,
        });
    }
}
