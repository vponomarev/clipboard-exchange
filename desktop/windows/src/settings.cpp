#include "settings.h"

#include <wincrypt.h>
#include <shlobj.h>

#include <vector>

namespace {
std::wstring DirectoryPath() {
    wchar_t overridePath[32768] = {};
    const DWORD overrideLength = GetEnvironmentVariableW(L"CLIPBOARD_EXCHANGE_SETTINGS_DIR",
                                                          overridePath, ARRAYSIZE(overridePath));
    if (overrideLength > 0 && overrideLength < ARRAYSIZE(overridePath)) return overridePath;
    wchar_t path[MAX_PATH] = {};
    if (FAILED(SHGetFolderPathW(nullptr, CSIDL_LOCAL_APPDATA | CSIDL_FLAG_CREATE, nullptr,
                                SHGFP_TYPE_CURRENT, path))) return L".";
    return std::wstring(path) + L"\\ClipboardExchange";
}

Hotkey ReadHotkey(const wchar_t* path, const wchar_t* key, const Hotkey& fallback) {
    wchar_t value[128] = {};
    GetPrivateProfileStringW(L"hotkeys", key, FormatHotkey(fallback).c_str(), value, 128, path);
    Hotkey parsed;
    return ParseHotkey(value, &parsed, nullptr) ? parsed : fallback;
}

bool ReadBool(const wchar_t* path, const wchar_t* key, bool fallback) {
    return GetPrivateProfileIntW(L"general", key, fallback ? 1 : 0, path) != 0;
}

bool ApplyStartup(bool enabled) {
    if (GetEnvironmentVariableW(L"CLIPBOARD_EXCHANGE_DISABLE_STARTUP_WRITE", nullptr, 0) > 0) return true;
    HKEY key = nullptr;
    if (RegCreateKeyExW(HKEY_CURRENT_USER, L"Software\\Microsoft\\Windows\\CurrentVersion\\Run",
                        0, nullptr, 0, KEY_SET_VALUE, nullptr, &key, nullptr) != ERROR_SUCCESS) return false;
    LONG result = ERROR_SUCCESS;
    if (enabled) {
        wchar_t executable[32768] = {};
        if (!GetModuleFileNameW(nullptr, executable, ARRAYSIZE(executable))) result = GetLastError();
        else {
            const std::wstring command = L"\"" + std::wstring(executable) + L"\" --background";
            result = RegSetValueExW(key, L"ClipboardExchange", 0, REG_SZ,
                                   reinterpret_cast<const BYTE*>(command.c_str()),
                                   static_cast<DWORD>((command.size() + 1) * sizeof(wchar_t)));
        }
    } else result = RegDeleteValueW(key, L"ClipboardExchange");
    RegCloseKey(key);
    return result == ERROR_SUCCESS || (!enabled && result == ERROR_FILE_NOT_FOUND);
}

bool Protect(const std::wstring& value, std::wstring* encoded) {
    if (!encoded) return false;
    DATA_BLOB input = {static_cast<DWORD>(value.size() * sizeof(wchar_t)),
                       reinterpret_cast<BYTE*>(const_cast<wchar_t*>(value.data()))};
    static const wchar_t purpose[] = L"ClipboardExchange.Win32.Settings.v1";
    DATA_BLOB entropy = {sizeof(purpose), reinterpret_cast<BYTE*>(const_cast<wchar_t*>(purpose))};
    DATA_BLOB output = {};
    if (!CryptProtectData(&input, L"Clipboard Exchange room URL", &entropy, nullptr, nullptr,
                          CRYPTPROTECT_UI_FORBIDDEN, &output)) return false;
    DWORD characters = 0;
    const DWORD flags = CRYPT_STRING_BASE64 | CRYPT_STRING_NOCRLF;
    bool ok = CryptBinaryToStringW(output.pbData, output.cbData, flags, nullptr, &characters) != FALSE;
    if (ok) {
        std::vector<wchar_t> buffer(characters);
        ok = CryptBinaryToStringW(output.pbData, output.cbData, flags, buffer.data(), &characters) != FALSE;
        if (ok) encoded->assign(buffer.data());
    }
    LocalFree(output.pbData);
    return ok;
}

bool Unprotect(const std::wstring& encoded, std::wstring* value) {
    if (!value || encoded.empty()) return false;
    DWORD bytes = 0;
    if (!CryptStringToBinaryW(encoded.c_str(), 0, CRYPT_STRING_BASE64, nullptr, &bytes, nullptr, nullptr)) return false;
    std::vector<BYTE> cipher(bytes);
    if (!CryptStringToBinaryW(encoded.c_str(), 0, CRYPT_STRING_BASE64, cipher.data(), &bytes, nullptr, nullptr)) return false;
    DATA_BLOB input = {bytes, cipher.data()};
    static const wchar_t purpose[] = L"ClipboardExchange.Win32.Settings.v1";
    DATA_BLOB entropy = {sizeof(purpose), reinterpret_cast<BYTE*>(const_cast<wchar_t*>(purpose))};
    DATA_BLOB output = {};
    if (!CryptUnprotectData(&input, nullptr, &entropy, nullptr, nullptr,
                            CRYPTPROTECT_UI_FORBIDDEN, &output)) return false;
    const bool valid = output.cbData % sizeof(wchar_t) == 0;
    if (valid) value->assign(reinterpret_cast<wchar_t*>(output.pbData), output.cbData / sizeof(wchar_t));
    SecureZeroMemory(output.pbData, output.cbData);
    LocalFree(output.pbData);
    return valid;
}
}

Settings DefaultSettings() {
    return {L"", {MOD_CONTROL | MOD_SHIFT | MOD_ALT | MOD_NOREPEAT, 'V'},
            {MOD_CONTROL | MOD_SHIFT | MOD_ALT | MOD_NOREPEAT, 'S'},
            {MOD_CONTROL | MOD_SHIFT | MOD_ALT | MOD_NOREPEAT, 'L'},
            {MOD_CONTROL | MOD_SHIFT | MOD_ALT | MOD_NOREPEAT, 'H'}, false, true};
}

std::wstring SettingsPath() { return DirectoryPath() + L"\\settings.ini"; }

Settings LoadSettings() {
    Settings settings = DefaultSettings();
    const std::wstring path = SettingsPath();
    wchar_t protectedRoom[8192] = {};
    GetPrivateProfileStringW(L"connection", L"roomUrlProtected", L"", protectedRoom,
                             ARRAYSIZE(protectedRoom), path.c_str());
    if (!Unprotect(protectedRoom, &settings.roomUrl)) {
        // One-time migration from native prototype versions that stored the URL plainly.
        wchar_t legacyRoom[2048] = {};
        GetPrivateProfileStringW(L"connection", L"roomUrl", L"", legacyRoom,
                                 ARRAYSIZE(legacyRoom), path.c_str());
        settings.roomUrl = legacyRoom;
    }
    settings.sendClipboard = ReadHotkey(path.c_str(), L"sendClipboard", settings.sendClipboard);
    settings.sendSelection = ReadHotkey(path.c_str(), L"sendSelection", settings.sendSelection);
    settings.showLatest = ReadHotkey(path.c_str(), L"showLatest", settings.showLatest);
    settings.showHistory = ReadHotkey(path.c_str(), L"showHistory", settings.showHistory);
    settings.startAtLogin = ReadBool(path.c_str(), L"startAtLogin", settings.startAtLogin);
    settings.notifications = ReadBool(path.c_str(), L"notifications", settings.notifications);
    return settings;
}

bool SaveSettings(const Settings& settings, std::wstring* error) {
    const std::wstring directory = DirectoryPath();
    if (!CreateDirectoryW(directory.c_str(), nullptr) && GetLastError() != ERROR_ALREADY_EXISTS) {
        if (error) *error = L"Не удалось создать каталог настроек";
        return false;
    }
    const std::wstring path = SettingsPath();
    std::wstring protectedRoom;
    if (!Protect(settings.roomUrl, &protectedRoom)) {
        if (error) *error = L"Windows DPAPI не смог защитить ссылку комнаты";
        return false;
    }
    const auto write = [&path](const wchar_t* section, const wchar_t* key, const std::wstring& value) {
        return WritePrivateProfileStringW(section, key, value.c_str(), path.c_str()) != FALSE;
    };
    const bool ok = write(L"connection", L"roomUrlProtected", protectedRoom) &&
        write(L"hotkeys", L"sendClipboard", FormatHotkey(settings.sendClipboard)) &&
        write(L"hotkeys", L"sendSelection", FormatHotkey(settings.sendSelection)) &&
        write(L"hotkeys", L"showLatest", FormatHotkey(settings.showLatest)) &&
        write(L"hotkeys", L"showHistory", FormatHotkey(settings.showHistory)) &&
        write(L"general", L"startAtLogin", settings.startAtLogin ? L"1" : L"0") &&
        write(L"general", L"notifications", settings.notifications ? L"1" : L"0") &&
        ApplyStartup(settings.startAtLogin);
    if (ok) WritePrivateProfileStringW(L"connection", L"roomUrl", nullptr, path.c_str());
    if (!ok && error) *error = L"Не удалось сохранить настройки";
    return ok;
}
