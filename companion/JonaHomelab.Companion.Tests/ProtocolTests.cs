using Microsoft.AspNetCore.Http;
using System.Text.Json;
using Xunit;

namespace JonaHomelab.Companion.Tests;

public sealed class ProtocolTests
{
    private static readonly byte[] Secret = Enumerable.Range(0, 32).Select(value => (byte)value).ToArray();
    private const string Nonce = "abcdefghijklmnopqrstuv";

    [Fact]
    public void Request_signature_round_trips_and_rejects_tampering()
    {
        const string body = "{\"force\":true}";
        const long timestamp = 1_777_777_777;
        var signature = Protocol.SignRequest(Secret, "POST", "/v1/shutdown", timestamp, Nonce, body);
        var headers = new HeaderDictionary
        {
            [Protocol.TimestampHeader] = timestamp.ToString(),
            [Protocol.NonceHeader] = Nonce,
            [Protocol.RequestSignatureHeader] = signature,
        };

        Assert.True(Protocol.VerifyRequest(Secret, "POST", "/v1/shutdown", body, headers, DateTimeOffset.FromUnixTimeSeconds(timestamp), out var parsedNonce));
        Assert.Equal(Nonce, parsedNonce);
        Assert.False(Protocol.VerifyRequest(Secret, "POST", "/v1/shutdown", "{\"force\":false}", headers, DateTimeOffset.FromUnixTimeSeconds(timestamp), out _));
        Assert.False(Protocol.VerifyRequest(Secret, "POST", "/v1/shutdown", body, headers, DateTimeOffset.FromUnixTimeSeconds(timestamp + 61), out _));
    }

    [Fact]
    public void Shared_protocol_vector_matches_node_client()
    {
        using var document = JsonDocument.Parse(File.ReadAllText(Path.Combine(AppContext.BaseDirectory, "companion-protocol.json")));
        var vector = document.RootElement;
        var secret = Convert.FromBase64String(vector.GetProperty("secret").GetString()!.Replace('-', '+').Replace('_', '/') + "=");
        Assert.Equal(vector.GetProperty("requestSignature").GetString(), Protocol.SignRequest(secret, vector.GetProperty("method").GetString()!, vector.GetProperty("path").GetString()!, vector.GetProperty("timestamp").GetInt64(), vector.GetProperty("nonce").GetString()!, vector.GetProperty("body").GetString()!));
        Assert.Equal(vector.GetProperty("responseSignature").GetString(), Protocol.SignResponse(secret, vector.GetProperty("responseStatus").GetInt32(), vector.GetProperty("nonce").GetString()!, vector.GetProperty("responseBody").GetString()!));
    }

    [Fact]
    public void Pairing_code_is_fixed_length_and_round_trips_secret()
    {
        var code = Protocol.PairingCode(Secret);
        Assert.Equal(49, code.Length);
        Assert.Equal(Secret, Protocol.ParsePairingCode(code));
        Assert.Throws<FormatException>(() => Protocol.ParsePairingCode(code[..^1] + "!"));
    }

    [Fact]
    public async Task Shutdown_executor_maps_force_and_enforces_cooldown()
    {
        var calls = new List<bool>();
        var executor = new ShutdownExecutor(calls.Add);
        Assert.True(executor.TrySchedule(false));
        Assert.False(executor.TrySchedule(true));
        await Task.Delay(450);
        Assert.Equal(new[] { false }, calls);
    }
}
