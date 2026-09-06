Unicode true
SetCompressor /SOLID lzma

!include "MUI2.nsh"
!include "WinMessages.nsh"

!ifndef APP_VERSION
  !define APP_VERSION "1.0.0"
!endif
!ifndef APP_VERSION_NUMERIC
  !define APP_VERSION_NUMERIC "1.0.0.0"
!endif

Name "Clipboard Exchange"
OutFile "build\clipboard-exchange-windows-x64-${APP_VERSION}-setup.exe"
InstallDir "$LOCALAPPDATA\Programs\Clipboard Exchange"
RequestExecutionLevel user

VIProductVersion "${APP_VERSION_NUMERIC}"
VIAddVersionKey "ProductName" "Clipboard Exchange"
VIAddVersionKey "FileDescription" "Native Win32 client installer"
VIAddVersionKey "FileVersion" "${APP_VERSION}"
VIAddVersionKey "LegalCopyright" "Clipboard Exchange contributors"
Icon "resources\app.ico"
UninstallIcon "resources\app.ico"

!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\clipboard-exchange.exe"
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "Russian"
!insertmacro MUI_LANGUAGE "English"

Function .onInit
  ; Preserve /D=... and the compiled default. Only recover if the shell folder
  ; lookup unexpectedly produced an empty path.
  StrCmp $INSTDIR "$LOCALAPPDATA\Programs\Clipboard Exchange" use_default
  StrCmp $INSTDIR "" use_default init_done
use_default:
  ReadEnvStr $1 "LOCALAPPDATA"
  StrCmp $1 "" 0 have_local_appdata
  StrCpy $1 "$PROFILE\AppData\Local"
have_local_appdata:
  StrCpy $INSTDIR "$1\Programs\Clipboard Exchange"
init_done:
FunctionEnd

Function CloseRunningApplication
  StrCpy $1 0
close_loop:
  FindWindow $0 "ClipboardExchangeMainWindow" ""
  IntCmp $0 0 close_done
  SendMessage $0 ${WM_COMMAND} 301 0 /TIMEOUT=2000
  Sleep 100
  IntOp $1 $1 + 1
  IntCmp $1 30 close_done close_loop close_done
close_done:
FunctionEnd

Function un.CloseRunningApplication
  StrCpy $1 0
un_close_loop:
  FindWindow $0 "ClipboardExchangeMainWindow" ""
  IntCmp $0 0 un_close_done
  SendMessage $0 ${WM_COMMAND} 301 0 /TIMEOUT=2000
  Sleep 100
  IntOp $1 $1 + 1
  IntCmp $1 30 un_close_done un_close_loop un_close_done
un_close_done:
FunctionEnd

Section "Clipboard Exchange" MainSection
  Call CloseRunningApplication
  SetOutPath "$INSTDIR"
  File "/oname=clipboard-exchange.exe" "build\clipboard-exchange-win32.exe"
  WriteRegStr HKCU "Software\ClipboardExchange" "InstallDir" "$INSTDIR"
  WriteUninstaller "$INSTDIR\uninstall.exe"

  CreateDirectory "$SMPROGRAMS\Clipboard Exchange"
  CreateShortcut "$SMPROGRAMS\Clipboard Exchange\Clipboard Exchange.lnk" "$INSTDIR\clipboard-exchange.exe"
  CreateShortcut "$SMPROGRAMS\Clipboard Exchange\Удалить Clipboard Exchange.lnk" "$INSTDIR\uninstall.exe"

  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\ClipboardExchange" "DisplayName" "Clipboard Exchange"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\ClipboardExchange" "DisplayVersion" "${APP_VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\ClipboardExchange" "Publisher" "Clipboard Exchange"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\ClipboardExchange" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\ClipboardExchange" "DisplayIcon" "$INSTDIR\clipboard-exchange.exe,0"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\ClipboardExchange" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\ClipboardExchange" "EstimatedSize" 512
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\ClipboardExchange" "NoModify" 1
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\ClipboardExchange" "NoRepair" 1

  WriteRegStr HKCU "Software\Classes\clipboard-exchange" "" "URL:Clipboard Exchange room"
  WriteRegStr HKCU "Software\Classes\clipboard-exchange" "URL Protocol" ""
  WriteRegStr HKCU "Software\Classes\clipboard-exchange\DefaultIcon" "" "$INSTDIR\clipboard-exchange.exe,0"
  WriteRegStr HKCU "Software\Classes\clipboard-exchange\shell\open\command" "" '"$INSTDIR\clipboard-exchange.exe" "%1"'
SectionEnd

Section "Uninstall"
  Call un.CloseRunningApplication
  Delete "$SMPROGRAMS\Clipboard Exchange\Clipboard Exchange.lnk"
  Delete "$SMPROGRAMS\Clipboard Exchange\Удалить Clipboard Exchange.lnk"
  RMDir "$SMPROGRAMS\Clipboard Exchange"
  Delete "$INSTDIR\clipboard-exchange.exe"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\ClipboardExchange"
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "ClipboardExchange"
  DeleteRegKey HKCU "Software\Classes\clipboard-exchange"
  DeleteRegKey HKCU "Software\ClipboardExchange"
SectionEnd
