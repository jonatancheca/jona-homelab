using System.IO.Pipes;
using System.Net.NetworkInformation;
using System.Net.Sockets;
using System.Text.Json;

namespace JonaHomelab.Companion;

public static class TrayApplication
{
    public static void Run()
    {
        ApplicationConfiguration.Initialize();
        using var context = new TrayContext();
        Application.Run(context);
    }

    private sealed class TrayContext : ApplicationContext
    {
        private readonly NotifyIcon icon;
        private Form? form;

        public TrayContext()
        {
            var menu = new ContextMenuStrip();
            menu.Items.Add("Open", null, (_, _) => OpenForm());
            menu.Items.Add("Copy pairing code", null, (_, _) => CopyPairingCode());
            menu.Items.Add("Check for updates", null, (_, _) => CheckUpdates());
            menu.Items.Add(new ToolStripSeparator());
            menu.Items.Add("Exit tray", null, (_, _) => ExitThread());
            icon = new NotifyIcon { Icon = SystemIcons.Application, Text = CompanionConfiguration.DisplayName, Visible = true, ContextMenuStrip = menu };
            icon.DoubleClick += (_, _) => OpenForm();
        }

        private void OpenForm()
        {
            if (form is { IsDisposed: false }) { form.Activate(); return; }
            form = new CompanionForm();
            form.Show();
        }

        private async void CopyPairingCode()
        {
            try { var info = await PipeClient.CallAsync("get-info"); Clipboard.SetText(info.GetProperty("pairingCode").GetString() ?? string.Empty); icon.ShowBalloonTip(2000, CompanionConfiguration.DisplayName, "Pairing code copied.", ToolTipIcon.Info); }
            catch (Exception error) { MessageBox.Show(error.Message, CompanionConfiguration.DisplayName, MessageBoxButtons.OK, MessageBoxIcon.Error); }
        }

        private async void CheckUpdates()
        {
            try { var result = await PipeClient.CallAsync("check-update"); icon.ShowBalloonTip(2000, CompanionConfiguration.DisplayName, result.GetProperty("scheduled").GetBoolean() ? "Update scheduled." : "Already up to date.", ToolTipIcon.Info); }
            catch (Exception error) { MessageBox.Show(error.Message, CompanionConfiguration.DisplayName, MessageBoxButtons.OK, MessageBoxIcon.Error); }
        }

        protected override void ExitThreadCore()
        {
            if (form is { IsDisposed: false }) form.Close();
            icon.Visible = false;
            icon.Dispose();
            base.ExitThreadCore();
        }
    }

    private sealed class CompanionForm : Form
    {
        private readonly Label details = new() { AutoSize = true, Dock = DockStyle.Fill, Padding = new Padding(14) };

        public CompanionForm()
        {
            Text = CompanionConfiguration.DisplayName;
            Width = 420;
            Height = 260;
            StartPosition = FormStartPosition.CenterScreen;
            var copy = new Button { Text = "Copy pairing code", AutoSize = true };
            var rotate = new Button { Text = "Rotate code", AutoSize = true };
            copy.Click += async (_, _) => await CopyPairingCode();
            rotate.Click += async (_, _) => await RotatePairingCode();
            var buttons = new FlowLayoutPanel { Dock = DockStyle.Bottom, Height = 48, Padding = new Padding(10), FlowDirection = FlowDirection.RightToLeft };
            buttons.Controls.Add(rotate); buttons.Controls.Add(copy);
            Controls.Add(details); Controls.Add(buttons);
            Shown += async (_, _) => await RefreshInfo();
        }

        private async Task CopyPairingCode()
        {
            try
            {
                var info = await PipeClient.CallAsync("get-info");
                Clipboard.SetText(info.GetProperty("pairingCode").GetString() ?? string.Empty);
                MessageBox.Show("Pairing code copied.", Text);
            }
            catch (Exception error) { ShowError(error); }
        }

        private async Task RotatePairingCode()
        {
            try
            {
                if (MessageBox.Show("The old code stops working immediately. Continue?", Text, MessageBoxButtons.YesNo, MessageBoxIcon.Warning) != DialogResult.Yes) return;
                var info = await PipeClient.CallAsync("rotate");
                Clipboard.SetText(info.GetProperty("pairingCode").GetString() ?? string.Empty);
                await RefreshInfo();
            }
            catch (Exception error) { ShowError(error); }
        }

        private void ShowError(Exception error) => MessageBox.Show(error.Message, Text, MessageBoxButtons.OK, MessageBoxIcon.Error);

        private async Task RefreshInfo()
        {
            try
            {
                var info = await PipeClient.CallAsync("get-info");
                details.Text = $"Status: ready\nVersion: {info.GetProperty("version").GetString()}\nIP: {LocalIpv4()}\nPort: {info.GetProperty("port").GetInt32()}\n\nPairing code:\n{info.GetProperty("pairingCode").GetString()}";
            }
            catch (Exception error) { details.Text = $"Service unavailable\n{error.Message}"; }
        }

        private static string LocalIpv4()
        {
            try
            {
                var addresses = NetworkInterface.GetAllNetworkInterfaces()
                    .Where(network => network.OperationalStatus == OperationalStatus.Up && network.NetworkInterfaceType != NetworkInterfaceType.Loopback)
                    .SelectMany(network => network.GetIPProperties().UnicastAddresses)
                    .Select(item => item.Address)
                    .Where(address => address.AddressFamily == AddressFamily.InterNetwork && !System.Net.IPAddress.IsLoopback(address))
                    .Select(address => address.ToString())
                    .Distinct(StringComparer.Ordinal)
                    .ToArray();
                return addresses.Length == 0 ? "Unavailable" : string.Join(", ", addresses);
            }
            catch { return "Unavailable"; }
        }
    }

    private static class PipeClient
    {
        public static async Task<JsonElement> CallAsync(string action)
        {
            Exception? lastError = null;
            for (var attempt = 0; attempt < 3; attempt++)
            {
                try { return await CallOnceAsync(action); }
                catch (Exception error) when (error is IOException or TimeoutException or UnauthorizedAccessException)
                {
                    lastError = error;
                    if (attempt < 2) await Task.Delay(500);
                }
            }

            throw new InvalidOperationException("Companion service unavailable. Ensure JonaHomelabCompanion is running.", lastError);
        }

        private static async Task<JsonElement> CallOnceAsync(string action)
        {
            using var pipe = new NamedPipeClientStream(".", PipeServer.PipeName, PipeDirection.InOut, PipeOptions.Asynchronous);
            await pipe.ConnectAsync(3000);
            using var writer = new StreamWriter(pipe) { AutoFlush = true };
            using var reader = new StreamReader(pipe);
            await writer.WriteLineAsync(JsonSerializer.Serialize(new { action }));
            using var document = JsonDocument.Parse(await reader.ReadLineAsync() ?? throw new IOException("Companion returned no response."));
            if (document.RootElement.TryGetProperty("error", out var error)) throw new InvalidOperationException(error.GetString());
            return document.RootElement.Clone();
        }
    }
}
