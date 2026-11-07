; TapperVPN for Windows: TapperVPN-Setup.exe (Inno Setup 6).
;
; GitHub builds this for every release (.github/workflows/windows-installer.yml)
; from that release's signed tappervpn-windows-amd64.zip, and attaches it to
; the release. To build it yourself: unzip the release zip into
; installer\payload, then
;
;   iscc /DAppVersion=0.2.4 installer\tappervpn.iss     -> dist\TapperVPN-Setup.exe
;
; What it does (the same as the old scripts\install-windows.ps1):
;   - copies the files to C:\Program Files\TapperVPN and adds it to PATH
;   - adds and starts the TapperVPN service (starts with Windows)
;   - adds the "TapperVPN Update" task (daily signed updates from GitHub)
;   - signs the PC in, with an auth key or by approving it on the server
; Uninstalling removes the service, task and PATH entry. The device's
; identity in C:\ProgramData\TapperVPN is kept, so reinstalling doesn't
; need a new sign-in.
;
; Silent install (e.g. for remote setup):
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
Source: "{#Payload}\wintun.dll"; DestDir: "{app}"; Flags: ignoreversion
Source: "update-task.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "tappervpn.ico"; DestDir: "{app}"; Flags: ignoreversion

[UninstallRun]
Filename: "powershell.exe"; Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\update-task.ps1"" -Uninstall"; Flags: runhidden waituntilterminated; RunOnceId: "RemoveUpdateTask"
Filename: "{app}\tappervpnd.exe"; Parameters: "uninstall-system-daemon"; Flags: runhidden waituntilterminated; RunOnceId: "RemoveService"

[UninstallDelete]
; Left behind by the auto-updater (it renames files it replaces).
Type: files; Name: "{app}\*.old"

[Run]
Filename: "{cmd}"; Parameters: "{code:SignInParams}"; Description: "Sign this PC in to TapperVPN now"; Flags: postinstall nowait skipifsilent; Check: OfferSignIn

[Code]
const
  ServiceName = 'TapperVPN';
  DefaultServer = 'https://vp.tapper.co';
  EnvKey = 'SYSTEM\CurrentControlSet\Control\Session Manager\Environment';

var
  SignInPage: TInputQueryWizardPage;
  WasInstalled: Boolean;
  SignedInWithKey: Boolean;

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

function Server(): String;
begin
  Result := Trim(SignInPage.Values[0]);
  if Result = '' then
    Result := DefaultServer;
end;

function AuthKey(): String;
begin
  Result := Trim(SignInPage.Values[1]);
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

{ --- Wizard --- }

procedure InitializeWizard();
begin
  WasInstalled := RegKeyExists(HKLM64,
    'Software\Microsoft\Windows\CurrentVersion\Uninstall\{{#AppGuid}}_is1');

  SignInPage := CreateInputQueryPage(wpWelcome,
    'Sign in', 'Connect this PC to your TapperVPN network.',
    'With an auth key, the PC joins by itself. Make one on the server with:' + #13#10 +
    '    sudo headscale preauthkeys create --user admin --expiration 1h' + #13#10#13#10 +
    'Or leave the key empty: after setup, a window shows a sign-in request ' +
    'to approve on the server (sudo headscale auth register ...).');
  SignInPage.Add('Control server:', False);
  SignInPage.Add('Auth key (optional):', True);
  SignInPage.Values[0] := ExpandConstant('{param:SERVER|' + DefaultServer + '}');
  SignInPage.Values[1] := ExpandConstant('{param:AUTHKEY|}');
end;

function ShouldSkipPage(PageID: Integer): Boolean;
begin
  { An upgrade keeps the existing sign-in. }
  Result := (PageID = SignInPage.ID) and WasInstalled;
end;

function NextButtonClick(CurPageID: Integer): Boolean;
var
  S: String;
begin
  Result := True;
  if CurPageID = SignInPage.ID then begin
    S := Lowercase(Server());
    if (Pos('https://', S) <> 1) and (Pos('http://', S) <> 1) then begin
      MsgBox('The control server must start with https:// or http://', mbError, MB_OK);
      Result := False;
    end;
  end;
end;

{ --- Install --- }

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  Result := '';
  { Stop a running copy so its files can be replaced. }
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
  { Pass the key in a file so it doesn't show in the process list. ({tmp} is
    removed when setup ends.) }
  KeyFile := ExpandConstant('{tmp}\authkey.txt');
  SaveStringToFile(KeyFile, AuthKey(), False);
  Params := '/c ""' + ExpandConstant('{app}\tappervpn.exe') + '" up --login-server=' + Server() +
    ' "--auth-key=file:' + KeyFile + '" --unattended --timeout=90s > "' + OutFile + '" 2>&1"';
  WizardForm.StatusLabel.Caption := 'Signing in to ' + Server() + '...';
  if Run(ExpandConstant('{cmd}'), Params) = 0 then
    SignedInWithKey := True
  else begin
    LoadStringFromFile(OutFile, Output);
    SuppressibleMsgBox('Signing in with the auth key didn''t work:' + #13#10#13#10 + String(Output) +
      #13#10 + 'You can sign in from the last page of setup instead.', mbError, MB_OK, IDOK);
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

{ Finish page: open a window that runs `tappervpn up` and shows the sign-in
  request to approve on the server. }
function OfferSignIn(): Boolean;
begin
  Result := (not WasInstalled) and (not SignedInWithKey);
end;

function SignInParams(Param: String): String;
begin
  Result := '/k "title TapperVPN sign-in & echo Approve this PC on the server with the headscale command' +
    ' shown at the link below (sudo headscale auth register ...). & echo. & "' +
    ExpandConstant('{app}\tappervpn.exe') + '" up --login-server=' + Server() + ' --unattended"';
end;

{ --- Uninstall --- }

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usPostUninstall then
    RemoveFromPath(ExpandConstant('{app}'));
end;
