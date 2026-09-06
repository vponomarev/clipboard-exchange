#pragma once

#include "hotkey.h"
#include <string>

struct Settings {
    std::wstring roomUrl;
    Hotkey sendClipboard;
    Hotkey sendSelection;
    Hotkey showLatest;
    Hotkey showHistory;
    bool startAtLogin;
    bool notifications;
};

Settings DefaultSettings();
Settings LoadSettings();
bool SaveSettings(const Settings& settings, std::wstring* error);
std::wstring SettingsPath();
