using System.Security.Cryptography;
using System.Text;
using Microsoft.AspNetCore.Http;

namespace JonaHomelab.Companion;

public static class Protocol
{
    public const string Version = "1";
    public const string RequestSignatureHeader = "X-Jona-Signature";
    public const string TimestampHeader = "X-Jona-Timestamp";
    public const string NonceHeader = "X-Jona-Nonce";
    public const string ResponseSignatureHeader = "X-Jona-Response-Signature";

    public static string PairingCode(ReadOnlySpan<byte> secret) => $"jhcp1_{Base64Url(secret)}";

    public static byte[] ParsePairingCode(string code)
    {
        if (!code.StartsWith("jhcp1_", StringComparison.Ordinal) || code.Length != 49)
            throw new FormatException("Invalid Companion pairing code.");
        var encoded = code[6..].Replace('-', '+').Replace('_', '/');
        var bytes = Convert.FromBase64String(encoded + new string('=', (4 - encoded.Length % 4) % 4));
        if (bytes.Length != 32) throw new FormatException("Invalid Companion pairing code.");
        return bytes;
    }

    public static string SignRequest(ReadOnlySpan<byte> secret, string method, string path, long timestamp, string nonce, string body)
        => Sign(secret, $"{method.ToUpperInvariant()}\n{path}\n{timestamp}\n{nonce}\n{Hash(body)}");

    public static string SignResponse(ReadOnlySpan<byte> secret, int status, string nonce, string body)
        => Sign(secret, $"{status}\n{nonce}\n{Hash(body)}");

    public static bool VerifyRequest(ReadOnlySpan<byte> secret, string method, string path, string body, IHeaderDictionary headers, DateTimeOffset now, out string nonce)
    {
        nonce = headers[NonceHeader].FirstOrDefault() ?? string.Empty;
        var timestampText = headers[TimestampHeader].FirstOrDefault();
        var signature = headers[RequestSignatureHeader].FirstOrDefault();
        if (!long.TryParse(timestampText, out var timestamp) || !IsNonce(nonce) || !IsHexSignature(signature)) return false;
        var current = now.ToUnixTimeSeconds();
        if (timestamp < current - 60 || timestamp > current + 60) return false;
        var expected = SignRequest(secret, method, path, timestamp, nonce, body);
        return CryptographicOperations.FixedTimeEquals(Encoding.ASCII.GetBytes(expected), Encoding.ASCII.GetBytes(signature!.ToLowerInvariant()));
    }

    public static bool VerifyResponse(ReadOnlySpan<byte> secret, int status, string nonce, string body, string? signature)
    {
        if (!IsHexSignature(signature)) return false;
        var expected = SignResponse(secret, status, nonce, body);
        return CryptographicOperations.FixedTimeEquals(Encoding.ASCII.GetBytes(expected), Encoding.ASCII.GetBytes(signature!.ToLowerInvariant()));
    }

    private static string Sign(ReadOnlySpan<byte> secret, string canonical)
        => Convert.ToHexString(HMACSHA256.HashData(secret, Encoding.UTF8.GetBytes(canonical))).ToLowerInvariant();

    private static string Hash(string body)
        => Convert.ToHexString(SHA256.HashData(Encoding.UTF8.GetBytes(body))).ToLowerInvariant();

    private static bool IsNonce(string nonce)
        => nonce.Length >= 20 && nonce.Length <= 24 && nonce.All(c => c is >= 'A' and <= 'Z' or >= 'a' and <= 'z' or >= '0' and <= '9' or '-' or '_');

    private static bool IsHexSignature(string? signature)
        => signature?.Length == 64 && signature.All(c => Uri.IsHexDigit(c));

    private static string Base64Url(ReadOnlySpan<byte> bytes)
        => Convert.ToBase64String(bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_');
}
