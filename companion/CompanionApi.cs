using System.Collections.Concurrent;
using System.Net;
using System.Text;
using System.Text.Json;
using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.Hosting;
using Microsoft.Extensions.Hosting;

namespace JonaHomelab.Companion;

public static class CompanionApi
{
    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web);

    public static async Task RunAsync(CancellationToken cancellationToken)
    {
        var configuration = CompanionConfiguration.LoadOrCreate();
        var builder = WebApplication.CreateBuilder();
        builder.Host.UseWindowsService(options => options.ServiceName = CompanionConfiguration.ServiceName);
        builder.Services.AddSingleton(configuration);
        builder.Services.AddSingleton<ShutdownExecutor>();
        builder.Services.AddSingleton<ReplayGuard>();
        builder.Services.AddSingleton<UpdateCoordinator>();
        builder.WebHost.UseKestrel(options => options.Listen(IPAddress.Any, configuration.ConfiguredPort));
        builder.Services.AddHostedService<PipeServer>();
        builder.Services.AddHostedService<PeriodicUpdateWorker>();
        var app = builder.Build();

        app.MapGet("/health", (HttpContext context) =>
        {
            if (!IsLoopback(context.Connection.RemoteIpAddress)) return Results.StatusCode(StatusCodes.Status403Forbidden);
            return Results.Json(new { status = "ok", version = CompanionConfiguration.ReleaseVersion });
        });
        app.MapGet("/v1/status", async (HttpContext context, CompanionConfiguration config, ReplayGuard replay) =>
            await HandleStatus(context, config, replay));
        app.MapPost("/v1/shutdown", async (HttpContext context, CompanionConfiguration config, ReplayGuard replay, ShutdownExecutor executor) =>
            await HandleShutdown(context, config, replay, executor));

        app.Lifetime.ApplicationStarted.Register(() =>
        {
            var coordinator = app.Services.GetRequiredService<UpdateCoordinator>();
            _ = coordinator.CheckAndScheduleAsync(CancellationToken.None);
        });
        await app.RunAsync(cancellationToken);
    }

    private static async Task<IResult> HandleStatus(HttpContext context, CompanionConfiguration config, ReplayGuard replay)
    {
        var auth = await Authenticate(context, config, replay, requireBody: false);
        if (!auth.Accepted) return Results.StatusCode(auth.StatusCode);
        var body = JsonSerializer.Serialize(new { ready = true, version = CompanionConfiguration.ReleaseVersion, accepted = true }, JsonOptions);
        return SignedJson(context, StatusCodes.Status200OK, body, config.ReadSecret(), auth.Nonce);
    }

    private static async Task<IResult> HandleShutdown(HttpContext context, CompanionConfiguration config, ReplayGuard replay, ShutdownExecutor executor)
    {
        var auth = await Authenticate(context, config, replay, requireBody: true);
        if (!auth.Accepted) return Results.StatusCode(auth.StatusCode);
        if (!TryReadForce(auth.Body, out var force)) return Results.StatusCode(StatusCodes.Status400BadRequest);
        var accepted = executor.TrySchedule(force);
        var status = accepted ? StatusCodes.Status202Accepted : StatusCodes.Status429TooManyRequests;
        var body = JsonSerializer.Serialize(new { accepted, retryAfter = accepted ? 10 : 10 }, JsonOptions);
        return SignedJson(context, status, body, config.ReadSecret(), auth.Nonce);
    }

    private static async Task<AuthResult> Authenticate(HttpContext context, CompanionConfiguration config, ReplayGuard replay, bool requireBody)
    {
        if (!IsPrivateClient(context.Connection.RemoteIpAddress)) return new(false, StatusCodes.Status403Forbidden, string.Empty, string.Empty);
        var body = requireBody ? await new StreamReader(context.Request.Body, Encoding.UTF8).ReadToEndAsync() : string.Empty;
        var secret = config.ReadSecret();
        if (!Protocol.VerifyRequest(secret, context.Request.Method, context.Request.Path, body, context.Request.Headers, DateTimeOffset.UtcNow, out var nonce))
            return new(false, StatusCodes.Status401Unauthorized, nonce, body);
        if (!replay.TryUse(nonce)) return new(false, StatusCodes.Status409Conflict, nonce, body);
        return new(true, StatusCodes.Status200OK, nonce, body);
    }

    private static IResult SignedJson(HttpContext context, int status, string body, ReadOnlySpan<byte> secret, string nonce)
    {
        context.Response.Headers[Protocol.ResponseSignatureHeader] = Protocol.SignResponse(secret, status, nonce, body);
        return Results.Text(body, "application/json", Encoding.UTF8, status);
    }

    private static bool TryReadForce(string body, out bool force)
    {
        force = false;
        try
        {
            using var document = JsonDocument.Parse(body);
            if (document.RootElement.ValueKind != JsonValueKind.Object || document.RootElement.EnumerateObject().Count() != 1 || !document.RootElement.TryGetProperty("force", out var property) || property.ValueKind is not (JsonValueKind.True or JsonValueKind.False)) return false;
            force = property.GetBoolean();
            return true;
        }
        catch (JsonException) { return false; }
    }

    private static bool IsLoopback(IPAddress? address)
    {
        if (address is null) return false;
        if (address.IsIPv4MappedToIPv6) address = address.MapToIPv4();
        return IPAddress.IsLoopback(address);
    }

    private static bool IsPrivateClient(IPAddress? address)
    {
        if (address is null) return false;
        if (address.IsIPv4MappedToIPv6) address = address.MapToIPv4();
        if (IPAddress.IsLoopback(address)) return true;
        var bytes = address.GetAddressBytes();
        return address.AddressFamily == System.Net.Sockets.AddressFamily.InterNetwork && (bytes[0] == 10 || bytes[0] == 192 && bytes[1] == 168 || bytes[0] == 172 && bytes[1] is >= 16 and <= 31);
    }

    private sealed record AuthResult(bool Accepted, int StatusCode, string Nonce, string Body);
}

public sealed class ReplayGuard
{
    private readonly ConcurrentDictionary<string, DateTimeOffset> nonces = new();

    public bool TryUse(string nonce)
    {
        var now = DateTimeOffset.UtcNow;
        foreach (var item in nonces.Where(item => now - item.Value > TimeSpan.FromMinutes(5))) nonces.TryRemove(item.Key, out _);
        return nonces.TryAdd(nonce, now);
    }
}

public sealed class PeriodicUpdateWorker : BackgroundService
{
    private readonly UpdateCoordinator updates;

    public PeriodicUpdateWorker(UpdateCoordinator updates) { this.updates = updates; }

    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        try
        {
            await Task.Delay(TimeSpan.FromHours(24), stoppingToken);
            while (!stoppingToken.IsCancellationRequested)
            {
                await updates.CheckAndScheduleAsync(stoppingToken);
                await Task.Delay(TimeSpan.FromHours(24), stoppingToken);
            }
        }
        catch (OperationCanceledException) when (stoppingToken.IsCancellationRequested) { }
    }
}
