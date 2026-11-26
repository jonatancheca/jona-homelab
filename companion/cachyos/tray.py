#!/usr/bin/python -I
import json
import re
import sys
import zipfile
from pathlib import Path

from PySide6.QtCore import QObject, QProcess, QTimer, QUrl, Slot
from PySide6.QtDBus import QDBusConnection, QDBusInterface
from PySide6.QtGui import QIcon
from PySide6.QtNetwork import QNetworkAccessManager, QNetworkProxy, QNetworkRequest
from PySide6.QtWidgets import (
    QApplication, QFileDialog, QLabel, QMenu, QMessageBox, QPushButton,
    QSystemTrayIcon, QVBoxLayout, QWidget,
)

TITLE = "Jona Homelab Companion"
BUS = "org.jonahomelab.CompanionTray"
BINARY = "/usr/bin/jona-homelab-companion"


class Window(QWidget):
    def __init__(self):
        super().__init__()
        self.setWindowTitle(TITLE)
        self.process = None
        self.health_pending = False
        self.status = QLabel("Conectando con el servicio…")
        self.notice = QLabel()
        self.notice.setWordWrap(True)
        layout = QVBoxLayout(self)
        layout.addWidget(self.status)
        self.buttons = []
        for label, action in (("Copiar código de emparejado", "pair"),
                              ("Rotar código…", "rotate"), ("Guardar diagnóstico…", "diagnostics")):
            button = QPushButton(label)
            button.clicked.connect(lambda checked=False, name=action: self.run_action(name))
            self.buttons.append(button)
            layout.addWidget(button)
        layout.addWidget(self.notice)
        self.network = QNetworkAccessManager(self)
        self.network.setProxy(QNetworkProxy(QNetworkProxy.ProxyType.NoProxy))
        self.network.finished.connect(self.health_finished)
        self.timer = QTimer(self)
        self.timer.timeout.connect(self.refresh)
        self.timer.start(5000)
        self.refresh()

    def refresh(self):
        if self.health_pending:
            return
        req = QNetworkRequest(QUrl("http://127.0.0.1:47654/health"))
        req.setTransferTimeout(2000)
        req.setAttribute(QNetworkRequest.Attribute.RedirectPolicyAttribute,
                         QNetworkRequest.RedirectPolicy.ManualRedirectPolicy)
        self.health_pending = True
        self.network.get(req)

    def health_finished(self, reply):
        self.health_pending = False
        label = "Servicio desconectado"
        try:
            data = json.loads(bytes(reply.readAll()))
            if data.get("status") == "ok" and data.get("simulated") is False:
                label = f"Conectado · Companion {data.get('displayVersion', '')}"
                if data.get("lastServerCall"):
                    label += f"\nÚltima petición: {data['lastServerCall']}"
        except (ValueError, AttributeError):
            pass
        self.status.setText(label)
        reply.deleteLater()

    def run_action(self, action):
        if self.process is not None:
            return
        if action == "rotate":
            answer = QMessageBox.question(self, TITLE, "El panel perderá acceso hasta que pegues el código nuevo. ¿Rotarlo?",
                                          QMessageBox.StandardButton.Yes | QMessageBox.StandardButton.Cancel,
                                          QMessageBox.StandardButton.Cancel)
            if answer != QMessageBox.StandardButton.Yes:
                return
        destination = ""
        if action == "diagnostics":
            destination, _ = QFileDialog.getSaveFileName(self, "Guardar diagnóstico",
                str(Path.home() / "companion-diagnostico.zip"), "ZIP (*.zip)")
            if not destination:
                return
        self.notice.setText("Esperando autorización…")
        for button in self.buttons:
            button.setEnabled(False)
        proc = QProcess(self)
        self.process = proc
        proc.finished.connect(lambda code, status: self.finish(proc, code, action, destination))
        proc.errorOccurred.connect(lambda error: self.finish(proc, 1, action, destination)
                                   if error == QProcess.ProcessError.FailedToStart else None)
        proc.start("/usr/bin/pkexec", [BINARY, action])

    def finish(self, proc, code, action, destination):
        if self.process is not proc:
            return
        try:
            if code != 0:
                raise ValueError("Acción cancelada o servicio no disponible.")
            output = bytes(proc.readAllStandardOutput())
            if action == "diagnostics":
                with zipfile.ZipFile(destination, "w", zipfile.ZIP_DEFLATED) as archive:
                    archive.writestr("diagnostico.json", output)
                self.notice.setText(f"Diagnóstico guardado en {destination}")
            else:
                key = output.decode().strip()
                if not re.fullmatch(r"jhcp1_[A-Za-z0-9_-]{43}", key):
                    raise ValueError("Código inválido.")
                QApplication.clipboard().setText(key)
                self.notice.setText("Código copiado. Pégalo en el panel.")
        except (OSError, ValueError) as error:
            self.notice.setText(str(error))
        finally:
            self.process = None
            proc.deleteLater()
            for button in self.buttons:
                button.setEnabled(True)


class Controller(QObject):
    @Slot()
    def showWindow(self):
        window.showNormal()
        window.raise_()
        window.activateWindow()


if __name__ == "__main__":
    app = QApplication(sys.argv)
    app.setApplicationName(TITLE)
    app.setQuitOnLastWindowClosed(False)
    bus = QDBusConnection.sessionBus()
    silent = "--autostart" in sys.argv
    if bus.isConnected() and not bus.registerService(BUS):
        if not silent:
            QDBusInterface(BUS, "/Tray", "", bus).call("showWindow")
        sys.exit(0)
    window = Window()
    controller = Controller()
    bus.registerObject("/Tray", controller, QDBusConnection.RegisterOption.ExportAllSlots)
    icon = QIcon.fromTheme("network-server", QIcon.fromTheme("computer"))
    window.setWindowIcon(icon)
    tray = QSystemTrayIcon(icon)
    tray.setToolTip(TITLE)
    menu = QMenu()
    menu.addAction("Abrir Companion", controller.showWindow)
    menu.addAction("Salir de la bandeja", app.quit)
    tray.setContextMenu(menu)
    tray.activated.connect(lambda reason: controller.showWindow()
                           if reason == QSystemTrayIcon.ActivationReason.Trigger else None)
    tray.show()
    if not silent:
        controller.showWindow()
    sys.exit(app.exec())
