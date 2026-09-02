namespace JonaHomelab.Companion;

internal static class Program
{
    [STAThread]
    private static async Task<int> Main(string[] args)
    {
        if (args.FirstOrDefault() == "--update") return await UpdateCoordinator.RunUpdaterAsync(args.Skip(1).ToArray());
        if (args.FirstOrDefault() == "--tray") { TrayApplication.Run(); return 0; }
        await CompanionApi.RunAsync(CancellationToken.None);
        return 0;
    }
}
