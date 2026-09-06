#include "hotkey.h"

#include <algorithm>
#include <cwctype>
#include <sstream>
#include <vector>

namespace {
std::wstring Trim(std::wstring value) {
    while (!value.empty() && iswspace(value.front())) value.erase(value.begin());
    while (!value.empty() && iswspace(value.back())) value.pop_back();
    return value;
}

std::wstring Upper(std::wstring value) {
    std::transform(value.begin(), value.end(), value.begin(), towupper);
    return value;
}
}

bool ParseHotkey(const std::wstring& text, Hotkey* result, std::wstring* error) {
    if (!result) return false;
    Hotkey parsed = {MOD_NOREPEAT, 0};
    std::wstringstream stream(text);
    std::wstring token;
    while (std::getline(stream, token, L'+')) {
        token = Upper(Trim(token));
        if (token == L"CTRL" || token == L"CONTROL") parsed.modifiers |= MOD_CONTROL;
        else if (token == L"SHIFT") parsed.modifiers |= MOD_SHIFT;
        else if (token == L"ALT") parsed.modifiers |= MOD_ALT;
        else if (token == L"WIN" || token == L"WINDOWS") parsed.modifiers |= MOD_WIN;
        else if (token.size() == 1 && ((token[0] >= L'A' && token[0] <= L'Z') ||
                                      (token[0] >= L'0' && token[0] <= L'9'))) {
            if (parsed.key) {
                if (error) *error = L"Укажите ровно одну основную клавишу";
                return false;
            }
            parsed.key = static_cast<UINT>(token[0]);
        } else if (token.size() >= 2 && token[0] == L'F') {
            wchar_t* end = nullptr;
            long number = wcstol(token.c_str() + 1, &end, 10);
            if (!end || *end || number < 1 || number > 24 || parsed.key) {
                if (error) *error = L"Некорректная функциональная клавиша";
                return false;
            }
            parsed.key = VK_F1 + static_cast<UINT>(number - 1);
        } else {
            if (error) *error = L"Поддерживаются Ctrl, Shift, Alt, Win, A-Z, 0-9 и F1-F24";
            return false;
        }
    }
    if (!parsed.key || !(parsed.modifiers & (MOD_CONTROL | MOD_ALT | MOD_SHIFT | MOD_WIN))) {
        if (error) *error = L"Хоткей должен содержать модификатор и основную клавишу";
        return false;
    }
    *result = parsed;
    return true;
}

std::wstring FormatHotkey(const Hotkey& hotkey) {
    std::wstring value;
    const auto append = [&value](const wchar_t* part) {
        if (!value.empty()) value += L"+";
        value += part;
    };
    if (hotkey.modifiers & MOD_CONTROL) append(L"Ctrl");
    if (hotkey.modifiers & MOD_SHIFT) append(L"Shift");
    if (hotkey.modifiers & MOD_ALT) append(L"Alt");
    if (hotkey.modifiers & MOD_WIN) append(L"Win");
    if (hotkey.key >= VK_F1 && hotkey.key <= VK_F24) {
        wchar_t key[8];
        wsprintfW(key, L"F%u", hotkey.key - VK_F1 + 1);
        append(key);
    } else {
        wchar_t key[2] = {static_cast<wchar_t>(hotkey.key), 0};
        append(key);
    }
    return value;
}
