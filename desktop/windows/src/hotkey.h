#pragma once

#include <windows.h>
#include <string>

struct Hotkey {
    UINT modifiers;
    UINT key;
};

bool ParseHotkey(const std::wstring& text, Hotkey* result, std::wstring* error);
std::wstring FormatHotkey(const Hotkey& hotkey);
