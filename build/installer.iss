; TaskFlow — установщик (Inno Setup 7)
; Сборка: build\make-installer.bat → dist\TaskFlow-Setup-1.3.1.exe
; Установка пер-юзерная (без UAC): %LOCALAPPDATA%\Programs\TaskFlow

#define MyAppName "TaskFlow"
#define MyAppVersion "1.3.1"
#define MyAppPublisher "TaskFlow"
#define MyAppExeName "TaskFlow.exe"
#define MyAppDescription "TaskFlow — трекер задач"

[Setup]
AppId={{8F4B2C1D-9A77-4E5F-B3E2-7D6C1A0T4F5L}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
DefaultDirName={localappdata}\Programs\{#MyAppName}
DefaultGroupName={#MyAppName}
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
OutputDir=..\dist
OutputBaseFilename=TaskFlow-Setup-{#MyAppVersion}
SetupIconFile=icon.ico
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
UninstallDisplayIcon={app}\{#MyAppExeName}
; Русский язык — первый в списке (язык по умолчанию на русской системе)
ShowLanguageDialog=auto

[Languages]
Name: "russian"; MessagesFile: "compiler:Languages\Russian.isl"
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"
Name: "autostart"; Description: "Запускать при входе в Windows"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Files]
Source: "..\bin\TaskFlow.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\build\icon.ico"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\README.md"; DestDir: "{app}"; Flags: ignoreversion skipifsourcedoesntexist

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"
Name: "{group}\{cm:UninstallProgram,{#MyAppName}}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon
Name: "{userstartup}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: autostart

[Run]
Filename: "{app}\{#MyAppExeName}"; Description: "{cm:LaunchProgram,{#MyAppName}}"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
; Данные пользователя (%LOCALAPPDATA%\TaskFlow) при удалении сохраняются.

[Messages]
russian.WelcomeLabel2=Это установит [name/ver] — трекер задач с канбан-доской, календарём и напоминаниями.%n%nВсе данные хранятся локально (SQLite).%n%nРекомендуется закрыть другие приложения перед продолжением.
