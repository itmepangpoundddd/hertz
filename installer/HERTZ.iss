; HERTZ desktop installer for Windows (Inno Setup 6/7).
; Build with: powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\build-installer.ps1

#ifndef MyAppVersion
  #define MyAppVersion "0.2.0"
#endif

#define MyAppName "HERTZ"
#define MyAppPublisher "HERTZ"
#define MyAppURL "https://github.com/itmepangpoundddd/hertz"
#define MyAppExeName "HERTZ.exe"

[Setup]
AppId={{1FC324C9-841A-471E-AE16-203168AF59FE}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppVerName={#MyAppName} {#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
AppUpdatesURL={#MyAppURL}
AppCopyright=Copyright (C) 2026 HERTZ
DefaultDirName={autopf}\HERTZ
DefaultGroupName=HERTZ
DisableProgramGroupPage=yes
DisableWelcomePage=no
AllowNoIcons=yes
UsePreviousAppDir=yes
LicenseFile=..\LICENSE
InfoAfterFile=INSTALL-NOTES.txt
OutputDir=..\dist
OutputBaseFilename=HERTZ-Setup-{#MyAppVersion}
SetupIconFile=..\cmd\hertz-admin\icon.ico
UninstallDisplayIcon={app}\HERTZ.exe
UninstallDisplayName={#MyAppName}
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
CloseApplications=yes
RestartApplications=no
SetupLogging=yes
VersionInfoVersion={#MyAppVersion}.0
VersionInfoCompany={#MyAppPublisher}
VersionInfoDescription={#MyAppName} installer
VersionInfoProductName={#MyAppName}

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked
Name: "startup"; Description: "Start HERTZ when I sign in"; GroupDescription: "Startup:"

[Files]
Source: "..\dist\HERTZ.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\LICENSE"; DestDir: "{app}"; Flags: ignoreversion
Source: "INSTALL-NOTES.txt"; DestDir: "{app}"; Flags: ignoreversion
Source: "firewall-add.cmd"; DestDir: "{app}"; Flags: ignoreversion
Source: "firewall-remove.cmd"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\HERTZ"; Filename: "{app}\HERTZ.exe"; WorkingDir: "{app}"
Name: "{group}\Uninstall HERTZ"; Filename: "{uninstallexe}"
Name: "{autodesktop}\HERTZ"; Filename: "{app}\HERTZ.exe"; WorkingDir: "{app}"; Tasks: desktopicon
Name: "{userstartup}\HERTZ"; Filename: "{app}\HERTZ.exe"; WorkingDir: "{app}"; Tasks: startup; Check: not IsAdminInstallMode
Name: "{commonstartup}\HERTZ"; Filename: "{app}\HERTZ.exe"; WorkingDir: "{app}"; Tasks: startup; Check: IsAdminInstallMode

[Run]
Filename: "{app}\firewall-add.cmd"; Flags: runhidden; Check: IsAdminInstallMode
Filename: "{app}\HERTZ.exe"; Description: "{cm:LaunchProgram,HERTZ}"; Flags: nowait postinstall skipifsilent

[UninstallRun]
Filename: "{app}\firewall-remove.cmd"; Flags: runhidden; RunOnceId: "HERTZFirewallRules"

[UninstallDelete]
Type: filesandordirs; Name: "{app}"
