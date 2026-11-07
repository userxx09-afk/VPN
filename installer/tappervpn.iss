; TapperVPN for Windows: TapperVPN-Setup.exe (Inno Setup 6).
;
; GitHub builds this for every release (.github/workflows/windows-installer.yml)
; from that release's signed tappervpn-windows-amd64.zip, and attaches it to
; the release. To build it yourself: unzip the release zip into
; installer\payload, then
;
;   iscc /DAppVersion=0.2.4 installer\tappervpn.iss     -> dist\TapperVPN-Setup.exe
;
; What it does:
;   - copies the files to C:\Program Files\TapperVPN and adds it to PATH
;   - adds and starts the TapperVPN service (starts with Windows)
;   - adds the "TapperVPN Update" task (daily signed updates from GitHub;
;     any user can start it from the app's "Install update" button)
;   - adds the TapperVPN app (tappervpn-gui.exe): Start menu entry, and the
;     taskbar icon at every Windows sign-in
;   - opens the app, where the PC signs in (server, optional auth key) like
;     the Android app
; Uninstalling removes the service, task, app, and PATH entry. The device's
; identity in C:\ProgramData\TapperVPN is kept, so reinstalling doesn't
; need a new sign-in.
;
; Silent install (e.g. for remote setup), signing in with an auth key:
;   TapperVPN-Setup.exe /VERYSILENT /AUTHKEY=<key> [/SERVER=<url>]

#ifndef AppVersion
  #define AppVersion "0.0.0"
#endif
#ifndef Payload
  #define Payload "payload"
#endif
#define AppGuid "22B48679-BE9A-4F15-AC52-1C21A1747101"

[Setup]
AppId={{{#AppGuid}}
AppName=TapperVPN
AppVersion={#AppVersion}
AppVerName=TapperVPN {#AppVersion}
AppPublisher=TapperVPN
AppPublisherURL=https://github.com/userxx09-afk/VPN
AppSupportURL=https://github.com/userxx09-afk/VPN
AppUpdatesURL=https://github.com/userxx09-afk/VPN/releases
VersionInfoVersion={#AppVersion}
DefaultDirName={autopf}\TapperVPN
DisableDirPage=yes
DisableProgramGroupPage=yes
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
PrivilegesRequired=admin
MinVersion=10.0
OutputDir=..\dist
OutputBaseFilename=TapperVPN-Setup
SetupIconFile=tappervpn.ico
UninstallDisplayIcon={app}\tappervpn.ico
UninstallDisplayName=TapperVPN
WizardStyle=modern
Compression=lzma2
SolidCompression=yes
ChangesEnvironment=yes
; The service is stopped in PrepareToInstall, not by Restart Manager.
CloseApplications=no

[Files]
Source: "{#Payload}\tappervpn.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#Payload}\tappervpnd.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#Payload}\tappervpn-updater.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#Payload}\tappervpn-gui.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#Payload}\wintun.dll"; DestDir: "{app}"; Flags: ignoreversion
Source: "update-task.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "tappervpn.ico"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\TapperVPN"; Filename: "{app}\tappervpn-gui.exe"; IconFilename: "{app}\tappervpn.ico"; Comment: "Connect, pick an exit node, see your devices"

[Registry]
; The app's taskbar icon starts when anyone signs in to Windows.
Root: HKLM64; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "TapperVPN"; ValueData: """{app}\tappervpn-gui.exe"" -background"; Flags: uninsdeletevalue

[UninstallRun]
Filename: "{sys}\taskkill.exe"; Parameters: "/F /IM tappervpn-gui.exe"; Flags: runhidden waituntilterminated; RunOnceId: "StopApp"
Filename: "powershell.exe"; Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\update-task.ps1"" -Uninstall"; Flags: runhidden waituntilterminated; RunOnceId: "RemoveUpdateTask"
Filename: "{app}\tappervpnd.exe"; Parameters: "uninstall-system-daemon"; Flags: runhidden waituntilterminated; RunOnceId: "RemoveService"

[UninstallDelete]
; Left behind by the auto-updater (it renames files it replaces).
Type: files; Name: "{app}\*.old"

[Run]
; Open the app for the user who ran setup: it shows the sign-in on a new
; install, and the connection on an upgrade. Silent installs start just the icon.
Filename: "{app}\tappervpn-gui.exe"; Description: "Open TapperVPN"; Flags: postinstall nowait runasoriginaluser; Check: not WizardSilent
Filename: "{app}\tappervpn-gui.exe"; Parameters: "-background"; Flags: nowait runasoriginaluser; Check: WizardSilent

[Code]
const
  ServiceName = 'TapperVPN';
  DefaultServer = 'https://vp.tapper.co';
  EnvKey = 'SYSTEM\CurrentControlSet\Control\Session Manager\Environment';

function Run(Exe, Params: String): Integer;
var
  Code: Integer;
begin
  if Exec(Exe, Params, '', SW_HIDE, ewWaitUntilTerminated, Code) then
    Result := Code
  else
    Result := -1;
end;

function ServiceExists(): Boolean;
begin
  Result := Run(ExpandConstant('{sys}\sc.exe'), 'query ' + ServiceName) = 0;
end;

{ /SERVER= and /AUTHKEY= on the command line, for silent installs. }
function Server(): String;
begin
  Result := Trim(ExpandConstant('{param:SERVER|' + DefaultServer + '}'));
end;

function AuthKey(): String;
begin
  Result := Trim(ExpandConstant('{param:AUTHKEY|}'));
end;

{ --- PATH --- }

procedure AddToPath(Dir: String);
var
  P: String;
begin
  if not RegQueryStringValue(HKLM, EnvKey, 'Path', P) then
    P := '';
  if Pos(';' + Uppercase(Dir) + ';', ';' + Uppercase(P) + ';') > 0 then
    Exit;
  if (P <> '') and (P[Length(P)] <> ';') then
    P := P + ';';
  RegWriteExpandStringValue(HKLM, EnvKey, 'Path', P + Dir);
end;

procedure RemoveFromPath(Dir: String);
var
  P: String;
  I: Integer;
begin
  if not RegQueryStringValue(HKLM, EnvKey, 'Path', P) then
    Exit;
  P := ';' + P + ';';
  I := Pos(';' + Uppercase(Dir) + ';', Uppercase(P));
  if I = 0 then
    Exit;
  Delete(P, I, Length(Dir) + 1);
  RegWriteExpandStringValue(HKLM, EnvKey, 'Path', Copy(P, 2, Length(P) - 2));
end;

{ --- Install --- }

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  Result := '';
  { Stop running copies so their files can be replaced. }
  Run(ExpandConstant('{sys}\taskkill.exe'), '/F /IM tappervpn-gui.exe');
  if ServiceExists() then
    Run(ExpandConstant('{sys}\net.exe'), 'stop ' + ServiceName);
end;

procedure WaitForDaemon();
var
  I: Integer;
begin
  for I := 1 to 30 do begin
    if Run(ExpandConstant('{app}\tappervpn.exe'), 'status --json') = 0 then
      Exit;
    Sleep(1000);
  end;
end;

procedure SignInWithKey();
var
  OutFile, KeyFile, Params: String;
  Output: AnsiString;
begin
  OutFile := ExpandConstant('{tmp}\signin.txt');
  // Pass the key in a file so it doesn't show in the process list. Setup's
  // temp folder, and the file with it, is removed when setup ends.
  KeyFile := ExpandConstant('{tmp}\authkey.txt');
  SaveStringToFile(KeyFile, AuthKey(), False);
  Params := '/c ""' + ExpandConstant('{app}\tappervpn.exe') + '" up --login-server=' + Server() +
    ' "--auth-key=file:' + KeyFile + '" --unattended --timeout=90s > "' + OutFile + '" 2>&1"';
  WizardForm.StatusLabel.Caption := 'Signing in to ' + Server() + '...';
  if Run(ExpandConstant('{cmd}'), Params) <> 0 then begin
    LoadStringFromFile(OutFile, Output);
    SuppressibleMsgBox('Signing in with the auth key didn''t work:' + #13#10#13#10 +
      String(Output) + #13#10 + 'Sign in from the TapperVPN app instead.',
      mbError, MB_OK, IDOK);
  end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  App, Daemon: String;
begin
  if CurStep <> ssPostInstall then
    Exit;
  App := ExpandConstant('{app}');
  Daemon := App + '\tappervpnd.exe';

  WizardForm.StatusLabel.Caption := 'Setting up the TapperVPN service...';
  if ServiceExists() then
    { Make sure an older install's service runs the files we just copied. }
    Run(ExpandConstant('{sys}\sc.exe'), 'config ' + ServiceName + ' binPath= "\"' + Daemon + '\""')
  else if Run(Daemon, 'install-system-daemon') <> 0 then
    SuppressibleMsgBox('Adding the TapperVPN service failed. Run setup again, or run' + #13#10 +
      '"' + Daemon + '" install-system-daemon' + #13#10 + 'from an Administrator prompt to see why.',
      mbError, MB_OK, IDOK);
  Run(ExpandConstant('{sys}\net.exe'), 'start ' + ServiceName);

  WizardForm.StatusLabel.Caption := 'Adding the daily update check...';
  Run('powershell.exe', '-NoProfile -ExecutionPolicy Bypass -File "' + App + '\update-task.ps1" -Install');

  AddToPath(App);

  if AuthKey() <> '' then begin
    WaitForDaemon();
    SignInWithKey();
  end;
end;

{ --- Uninstall --- }

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usPostUninstall then
    RemoveFromPath(ExpandConstant('{app}'));
end;
