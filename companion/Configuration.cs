using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace JonaHomelab.Companion;

public sealed class CompanionConfiguration
{
    public const int Port = 47654;
    public const string ServiceName = "JonaHomelabCompanion";
    public const string DisplayName = "Jona Homelab Companion";
    public static readonly string DataDirectory = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData), "JonaHomelabCompanion");
    public static readonly string ConfigurationPath = Path.Combine(DataDirectory, "config.json");

    [JsonPropertyName("encryptedSecret")]
    public string EncryptedSecret { get; set; } = string.Empty;

    [JsonPropertyName("port")]
    public int ConfiguredPort { get; set; } = Port;

    public static CompanionConfiguration LoadOrCreate()
    {
        Directory.CreateDirectory(DataDirectory);
        if (File.Exists(ConfigurationPath))
        {
            var existing = JsonSerializer.Deserialize<CompanionConfiguration>(File.ReadAllText(ConfigurationPath));
            if (existing is not null && existing.ConfiguredPort == Port && !string.IsNullOrWhiteSpace(existing.EncryptedSecret)) return existing;
        }

        var secret = RandomNumberGenerator.GetBytes(32);
        var config = new CompanionConfiguration { EncryptedSecret = Convert.ToBase64String(ProtectedData.Protect(secret, Encoding.UTF8.GetBytes("jona-homelab-companion"), DataProtectionScope.LocalMachine)) };
        Save(config);
        return config;
    }

    public byte[] ReadSecret()
    {
        try { return ProtectedData.Unprotect(Convert.FromBase64String(EncryptedSecret), Encoding.UTF8.GetBytes("jona-homelab-companion"), DataProtectionScope.LocalMachine); }
        catch (Exception error) { throw new InvalidOperationException("Companion secret cannot be decrypted.", error); }
    }

    public string PairingCode() => Protocol.PairingCode(ReadSecret());

    public string RotateSecret()
    {
        var secret = RandomNumberGenerator.GetBytes(32);
        EncryptedSecret = Convert.ToBase64String(ProtectedData.Protect(secret, Encoding.UTF8.GetBytes("jona-homelab-companion"), DataProtectionScope.LocalMachine));
        Save(this);
        return Protocol.PairingCode(secret);
    }

    public static void Save(CompanionConfiguration config)
    {
        Directory.CreateDirectory(DataDirectory);
        var temporary = ConfigurationPath + ".tmp";
        File.WriteAllText(temporary, JsonSerializer.Serialize(config, new JsonSerializerOptions { WriteIndented = true }) + Environment.NewLine, new UTF8Encoding(false));
        File.Move(temporary, ConfigurationPath, true);
    }

    public static string ReleaseVersion
    {
        get
        {
            var path = Path.Combine(AppContext.BaseDirectory, "RELEASE_VERSION");
            return File.Exists(path) ? File.ReadAllText(path).Trim() : "dev";
        }
    }
}
