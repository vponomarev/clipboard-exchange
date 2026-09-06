#include "native_input.h"

#ifndef _MSC_VER
#error The production UI Automation implementation requires MSVC and the official Windows SDK.
#endif

#include <UIAutomation.h>
#include <oleauto.h>
#include <wrl/client.h>

#include <algorithm>
#include <vector>

using Microsoft::WRL::ComPtr;

namespace {
bool ReadSelectedTextImpl(HWND target, std::wstring* text, std::wstring* error) {
    if (!text) return false;
    text->clear();

    ComPtr<IUIAutomation> automation;
    ComPtr<IUIAutomationElement> element;
    ComPtr<IUIAutomationTextPattern> pattern;
    ComPtr<IUIAutomationTextRangeArray> ranges;
    const wchar_t* stage = L"CoCreateInstance";
    HRESULT result = CoCreateInstance(CLSID_CUIAutomation8, nullptr, CLSCTX_INPROC_SERVER,
                                      IID_PPV_ARGS(automation.GetAddressOf()));
    if (SUCCEEDED(result) && !automation) result = E_POINTER;
    if (SUCCEEDED(result)) {
        stage = target ? L"ElementFromHandle" : L"GetFocusedElement";
        result = target ? automation->ElementFromHandle(target, element.GetAddressOf())
                        : automation->GetFocusedElement(element.GetAddressOf());
    }
    if (SUCCEEDED(result) && !element) result = UIA_E_ELEMENTNOTAVAILABLE;

    VARIANT isPassword;
    VariantInit(&isPassword);
    if (SUCCEEDED(result)) {
        stage = L"GetCurrentPropertyValue(IsPassword)";
        result = element->GetCurrentPropertyValue(UIA_IsPasswordPropertyId, &isPassword);
    }
    if (SUCCEEDED(result) && isPassword.vt == VT_BOOL && isPassword.boolVal != VARIANT_FALSE) {
        VariantClear(&isPassword);
        if (error) *error = L"Выделение из поля пароля не отправляется";
        return false;
    }
    VariantClear(&isPassword);

    if (SUCCEEDED(result)) {
        stage = L"GetCurrentPattern(TextPattern)";
        ComPtr<IUnknown> unknown;
        result = element->GetCurrentPattern(UIA_TextPatternId, unknown.GetAddressOf());
        if (SUCCEEDED(result) && !unknown) result = UIA_E_NOTSUPPORTED;
        if (SUCCEEDED(result)) {
            stage = L"QueryInterface(IUIAutomationTextPattern)";
            result = unknown.As(&pattern);
        }
    }
    if (SUCCEEDED(result) && !pattern) result = UIA_E_NOTSUPPORTED;
    if (SUCCEEDED(result)) {
        SupportedTextSelection support = SupportedTextSelection_None;
        stage = L"TextPattern.SupportedTextSelection";
        result = pattern->get_SupportedTextSelection(&support);
        if (SUCCEEDED(result) && support == SupportedTextSelection_None)
            result = UIA_E_NOTSUPPORTED;
    }
    if (SUCCEEDED(result)) {
        stage = L"TextPattern.GetSelection";
        result = pattern->GetSelection(ranges.GetAddressOf());
    }
    if (SUCCEEDED(result) && !ranges) result = UIA_E_NOTSUPPORTED;

    int count = 0;
    if (SUCCEEDED(result)) {
        stage = L"TextRangeArray.Length";
        result = ranges->get_Length(&count);
    }
    for (int index = 0; SUCCEEDED(result) && index < count; ++index) {
        ComPtr<IUIAutomationTextRange> range;
        BSTR value = nullptr;
        stage = L"TextRangeArray.GetElement";
        result = ranges->GetElement(index, range.GetAddressOf());
        if (SUCCEEDED(result) && !range) result = UIA_E_NOTSUPPORTED;
        if (SUCCEEDED(result)) {
            stage = L"TextRange.GetText";
            result = range->GetText(-1, &value);
        }
        if (SUCCEEDED(result) && value) {
            if (!text->empty()) *text += L"\n";
            text->append(value, SysStringLen(value));
        }
        if (value) SysFreeString(value);
    }
    if (FAILED(result)) {
        if (error) {
            wchar_t code[32] = {};
            _snwprintf_s(code, ARRAYSIZE(code), _TRUNCATE, L" (0x%08X)",
                         static_cast<unsigned int>(result));
            *error = std::wstring(L"UI Automation: ") + stage + code;
        }
        return false;
    }
    if (text->empty()) {
        if (error) *error = L"Нет выделенного текста";
        return false;
    }
    return true;
}
}

bool ReadClipboardText(HWND owner, std::wstring* text, std::wstring* error) {
    if (!text) return false;
    if (!OpenClipboard(owner)) {
        if (error) *error = L"Системный буфер занят другим приложением";
        return false;
    }
    HANDLE handle = GetClipboardData(CF_UNICODETEXT);
    if (!handle) {
        CloseClipboard();
        if (error) *error = L"В буфере нет текста";
        return false;
    }
    const wchar_t* value = static_cast<const wchar_t*>(GlobalLock(handle));
    if (!value) {
        CloseClipboard();
        if (error) *error = L"Не удалось прочитать текст из буфера";
        return false;
    }
    *text = value;
    GlobalUnlock(handle);
    CloseClipboard();
    return true;
}

bool ReadSelectedText(std::wstring* text, std::wstring* error) {
    return ReadSelectedTextImpl(nullptr, text, error);
}

bool ReadSelectedTextFromWindow(HWND target, std::wstring* text, std::wstring* error) {
    if (!target) {
        if (error) *error = L"UI Automation: отсутствует целевое окно";
        return false;
    }
    return ReadSelectedTextImpl(target, text, error);
}

bool InsertUnicodeText(const std::wstring& text, std::wstring* error) {
    if (text.empty()) return true;
    std::vector<INPUT> input;
    input.reserve(text.size() * 2);
    for (size_t index = 0; index < text.size(); ++index) {
        const wchar_t character = text[index];
        INPUT down = {};
        down.type = INPUT_KEYBOARD;
        if (character == L'\r' || character == L'\n') {
            if (character == L'\r' && index + 1 < text.size() && text[index + 1] == L'\n') ++index;
            down.ki.wVk = VK_RETURN;
        } else {
            down.ki.wScan = character;
            down.ki.dwFlags = KEYEVENTF_UNICODE;
        }
        INPUT up = down;
        up.ki.dwFlags |= KEYEVENTF_KEYUP;
        input.push_back(down);
        input.push_back(up);
    }
    size_t offset = 0;
    while (offset < input.size()) {
        const UINT count = static_cast<UINT>(std::min<size_t>(input.size() - offset, 512));
        const UINT sent = SendInput(count, input.data() + offset, sizeof(INPUT));
        if (sent != count) {
            if (error) *error = L"Windows заблокировал эмуляцию ввода (" +
                                std::to_wstring(sent) + L"/" + std::to_wstring(count) +
                                L", код " + std::to_wstring(GetLastError()) + L")";
            return false;
        }
        offset += sent;
    }
    return true;
}
