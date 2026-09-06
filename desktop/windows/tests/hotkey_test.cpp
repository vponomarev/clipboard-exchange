#include "hotkey.h"

#include <iostream>

namespace {
int failures = 0;

void Check(bool condition, const char* message) {
    if (!condition) {
        std::cerr << "FAIL: " << message << "\n";
        ++failures;
    }
}
}

int main() {
    Hotkey hotkey;
    std::wstring error;
    Check(ParseHotkey(L"Ctrl+Shift+Alt+H", &hotkey, &error), "default history hotkey parses");
    Check(hotkey.key == 'H', "history key is H");
    Check((hotkey.modifiers & (MOD_CONTROL | MOD_SHIFT | MOD_ALT)) ==
          (MOD_CONTROL | MOD_SHIFT | MOD_ALT), "three modifiers are retained");
    Check(FormatHotkey(hotkey) == L"Ctrl+Shift+Alt+H", "hotkey formats canonically");
    Check(ParseHotkey(L"Win+F24", &hotkey, &error), "function key parses");
    Check(hotkey.key == VK_F24, "F24 maps correctly");
    Check(!ParseHotkey(L"H", &hotkey, &error), "bare key is rejected");
    Check(!ParseHotkey(L"Ctrl+H+J", &hotkey, &error), "two primary keys are rejected");
    Check(!ParseHotkey(L"Ctrl+Mouse1", &hotkey, &error), "unsupported key is rejected");
    if (!failures) std::cout << "hotkey tests passed\n";
    return failures ? 1 : 0;
}
