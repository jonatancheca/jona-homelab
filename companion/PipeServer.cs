using System.IO.Pipes;
using System.Security.AccessControl;
using System.Security.Principal;
using System.Text.Json;
using Microsoft.Extensions.Hosting;

namespace JonaHomelab.Companion;

public sealed class PipeServer : BackgroundService
{
    public const string PipeName = "JonaHomelabCompanion";
    private readonly CompanionConfiguration configuration;
    private readonly UpdateCoordinator updates;
    private readonly ILogger<PipeServer> logger;

    public PipeServer(CompanionConfiguration configuration, UpdateCoordinator updates, ILogger<PipeServer> logger)
    {
        this.configuration = configuration;
        this.updates = updates;
        this.logger = logger;
    }

    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        while (!stoppingToken.IsCancellationRequested)
        {
            try
            {
                using var pipe = CreatePipe();
                await pipe.WaitForConnectionAsync(stoppingToken);
                using var reader = new StreamReader(pipe);
                using var writer = new StreamWriter(pipe) { AutoFlush = true };
                var request = await reader.ReadLineAsync(stoppingToken);
                if (string.IsNullOrWhiteSpace(request)) continue;
                var response = await Handle(request, stoppingToken);
                await writer.WriteLineAsync(response);
            }
            catch (OperationCanceledException) when (stoppingToken.IsCancellationRequested) { }
            catch (Exception error)
            {
                logger.LogWarning(error, "Named pipe request failed.");
                await Task.Delay(500, stoppingToken);
            }
        }
    }

    private async Task<string> Handle(string request, CancellationToken cancellationToken)
    {
        try
        {
            using var document = JsonDocument.Parse(request);
            var action = document.RootElement.GetProperty("action").GetString();
            return action switch
            {
                "get-info" => JsonSerializer.Serialize(new { ready = true, version = CompanionConfiguration.ReleaseVersion, port = configuration.ConfiguredPort, pairingCode = configuration.PairingCode() }),
                "rotate" => JsonSerializer.Serialize(new { ready = true, pairingCode = configuration.RotateSecret() }),
                "check-update" => JsonSerializer.Serialize(new { scheduled = await updates.CheckAndScheduleAsync(cancellationToken) }),
                _ => JsonSerializer.Serialize(new { error = "Unknown action." }),
            };
        }
        catch { return JsonSerializer.Serialize(new { error = "Invalid local request." }); }
    }

    private static NamedPipeServerStream CreatePipe()
    {
        var security = new PipeSecurity();
        security.AddAccessRule(new PipeAccessRule(
            new SecurityIdentifier(WellKnownSidType.BuiltinUsersSid, null),
            PipeAccessRights.ReadWrite | PipeAccessRights.CreateNewInstance,
            AccessControlType.Allow));
        return NamedPipeServerStreamAcl.Create(PipeName, PipeDirection.InOut, 1, PipeTransmissionMode.Byte, PipeOptions.Asynchronous, 4096, 4096, security);
    }
}
