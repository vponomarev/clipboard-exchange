#pragma once

#include <windows.h>
#include <string>

bool ReadClipboardText(HWND owner, std::wstring* text, std::wstring* error);
bool ReadSelectedText(std::wstring* text, std::wstring* error);
// Explicit HWND variant used by deterministic UI Automation integration tests.
bool ReadSelectedTextFromWindow(HWND target, std::wstring* text, std::wstring* error);
bool InsertUnicodeText(const std::wstring& text, std::wstring* error);
