#include "hotkey.h"
#include "deep_link.h"
#include "history_cache.h"
#include "native_input.h"
#include "room_client.h"
#include "room_events.h"
#include "settings.h"

#include <commctrl.h>
#include <windowsx.h>
#include <process.h>
#include <shellapi.h>

#include <algorithm>
#include <sstream>
#include <string>
#include <vector>

#ifdef _MSC_VER
#pragma comment(linker, "\"/manifestdependency:type='win32' name='Microsoft.Windows.Common-Controls' version='6.0.0.0' processorArchitecture='*' publicKeyToken='6595b64144ccf1df' language='*'\"")
#endif

namespace {
constexpr wchar_t kMainClass[] = L"ClipboardExchangeMainWindow";
constexpr wchar_t kPickerClass[] = L"ClipboardExchangePickerWindow";
constexpr wchar_t kPreviewClass[] = L"ClipboardExchangePreviewWindow";
constexpr UINT kTrayMessage = WM_APP + 1;
constexpr UINT kNetworkMessage = WM_APP + 2;
constexpr UINT kRoomEventMessage = WM_APP + 3;
constexpr UINT kTrayId = 1;
constexpr UINT_PTR kInsertTimer = 1;
constexpr UINT_PTR kRefreshTimer = 2;
constexpr UINT_PTR kPreviewHoverTimer = 3;

enum ControlId {
    ID_ROOM_URL = 100,
    ID_SAVE = 101,
    ID_STATUS = 102,
    ID_OPEN_BROWSER = 103,
    ID_RESET_HOTKEYS = 104,
    ID_HOTKEY_CLIPBOARD = 110,
    ID_HOTKEY_SELECTION = 111,
    ID_HOTKEY_LATEST = 112,
    ID_HOTKEY_HISTORY = 113,
    ID_START_AT_LOGIN = 120,
    ID_NOTIFICATIONS = 121,
    ID_PICKER_LIST = 200,
    ID_TRAY_OPEN = 300,
    ID_TRAY_EXIT = 301,
    ID_TRAY_BROWSER = 302
};

enum HotkeyId {
    HK_SEND_CLIPBOARD = 1,
    HK_SEND_SELECTION = 2,
    HK_SHOW_LATEST = 3,
    HK_SHOW_HISTORY = 4
};

HINSTANCE g_instance = nullptr;
HWND g_main = nullptr;
HWND g_picker = nullptr;
HWND g_pickerList = nullptr;
HWND g_preview = nullptr;
HWND g_previewEdit = nullptr;
HWND g_pickerTarget = nullptr;
HFONT g_font = nullptr;
HANDLE g_singleInstance = nullptr;
int g_dpi = 96;
Settings g_settings;
std::vector<std::wstring> g_history;
std::vector<std::wstring> g_pickerHistory;
std::wstring g_pendingInsertion;
HWND g_pendingTarget = nullptr;
bool g_exiting = false;
bool g_pickerDirty = false;
volatile LONG g_refreshActive = 0;
volatile LONG g_generation = 1;
volatile LONG g_eventGeneration = 0;
bool g_realtimeUnsupported = false;
bool g_hotkeyCaptureActive = false;
bool g_previewActivated = false;
size_t g_previewHistoryIndex = static_cast<size_t>(-1);
ULONGLONG g_lastSnapshotTick = 0;

void SetStatus(const std::wstring& value);
void RefreshPickerList();
void UnregisterAllHotkeys();
bool RegisterAllHotkeys(const Settings& settings, std::wstring* error);

std::vector<std::wstring> MessageLines(const std::wstring& message) {
    std::vector<std::wstring> lines(1);
    for (wchar_t character : message) {
        if (character == L'\n') lines.emplace_back();
        else if (character != L'\r') lines.back() += character;
    }
    return lines;
}

size_t VisibleMessageLineCount(const std::wstring& message) {
    return std::min<size_t>(MessageLines(message).size(), 6);
}

bool MessageIsTruncated(const std::wstring& message) {
    return MessageLines(message).size() > 6;
}

std::wstring WindowsEditText(const std::wstring& message) {
    std::wstring result;
    result.reserve(message.size() + 16);
    for (size_t index = 0; index < message.size(); ++index) {
        const wchar_t character = message[index];
        if (character == L'\r') {
            result += L"\r\n";
            if (index + 1 < message.size() && message[index + 1] == L'\n') ++index;
        } else if (character == L'\n') result += L"\r\n";
        else result += character;
    }
    return result;
}

int Px(int value) { return MulDiv(value, g_dpi, 96); }

bool PresentIncomingRoom(HWND window, const std::wstring& commandLine) {
    const std::wstring target = ExtractRoomArgument(commandLine);
    if (target.empty()) return false;
    SetWindowTextW(GetDlgItem(window, ID_ROOM_URL), target.c_str());
    SetStatus(L"Получена ссылка комнаты. Проверьте её и нажмите «Сохранить».");
    ShowWindow(window, SW_SHOWNORMAL);
    SetForegroundWindow(window);
    return true;
}

struct NetworkTask {
    HWND notify;
    LONG generation;
    bool send;
    bool captureSelection;
    std::wstring roomUrl;
    std::wstring text;
};

struct NetworkResult {
    LONG generation;
    bool send;
    bool ok;
    RoomSnapshot snapshot;
    std::wstring error;
};

struct EventTask {
    HWND notify;
    LONG generation;
    std::wstring roomUrl;
};

unsigned __stdcall NetworkWorker(void* parameter) {
    NetworkTask* task = static_cast<NetworkTask*>(parameter);
    NetworkResult* result = new NetworkResult();
    result->generation = task->generation;
    result->send = task->send;
    const HRESULT com = CoInitializeEx(nullptr, COINIT_MULTITHREADED);
    if (task->captureSelection && FAILED(com)) {
        result->ok = false;
        result->error = L"Windows не смог инициализировать UI Automation";
    } else if (task->captureSelection) result->ok = ReadSelectedText(&task->text, &result->error);
    else result->ok = true;
    result->ok = result->ok && (!task->send || SendRoomText(task->roomUrl, task->text, &result->error));
    if (result->ok) result->ok = FetchRoom(task->roomUrl, &result->snapshot, &result->error);
    if (result->ok) SaveHistoryCache(task->roomUrl, result->snapshot.messages);
    if (!task->send) InterlockedExchange(&g_refreshActive, 0);
    if (SUCCEEDED(com)) CoUninitialize();
    if (!PostMessageW(task->notify, kNetworkMessage, 0, reinterpret_cast<LPARAM>(result))) delete result;
    delete task;
    return 0;
}

bool StartNetworkTask(bool send, const std::wstring& text = {}, bool captureSelection = false) {
    if (g_settings.roomUrl.empty()) {
        SetStatus(L"Сначала сохраните ссылку комнаты");
        return false;
    }
    if (!send && InterlockedCompareExchange(&g_refreshActive, 1, 0) != 0) return false;
    NetworkTask* task = new NetworkTask{g_main, g_generation, send, captureSelection,
                                        g_settings.roomUrl, text};
    uintptr_t thread = _beginthreadex(nullptr, 0, NetworkWorker, task, 0, nullptr);
    if (!thread) {
        if (!send) InterlockedExchange(&g_refreshActive, 0);
        delete task;
        SetStatus(L"Не удалось запустить сетевой поток");
        return false;
    }
    CloseHandle(reinterpret_cast<HANDLE>(thread));
    return true;
}

unsigned __stdcall EventWorker(void* parameter) {
    EventTask* task = static_cast<EventTask*>(parameter);
    const RoomEventResult result = WaitForRoomRefresh(task->roomUrl);
    PostMessageW(task->notify, kRoomEventMessage, static_cast<WPARAM>(result), task->generation);
    delete task;
    return 0;
}

void StartEventTask() {
    if (g_settings.roomUrl.empty() || g_realtimeUnsupported) return;
    const LONG generation = g_generation;
    if (InterlockedCompareExchange(&g_eventGeneration, generation, 0) != 0) return;
    EventTask* task = new EventTask{g_main, generation, g_settings.roomUrl};
    uintptr_t thread = _beginthreadex(nullptr, 0, EventWorker, task, 0, nullptr);
    if (!thread) {
        InterlockedCompareExchange(&g_eventGeneration, 0, generation);
        delete task;
        return;
    }
    CloseHandle(reinterpret_cast<HANDLE>(thread));
}

std::wstring WindowText(HWND window) {
    const int length = GetWindowTextLengthW(window);
    std::vector<wchar_t> value(static_cast<size_t>(length) + 1);
    GetWindowTextW(window, value.data(), static_cast<int>(value.size()));
    return value.data();
}

void SetStatus(const std::wstring& value) {
    SetWindowTextW(GetDlgItem(g_main, ID_STATUS), value.c_str());
    NOTIFYICONDATAW icon = {};
    icon.cbSize = sizeof(icon);
    icon.hWnd = g_main;
    icon.uID = kTrayId;
    icon.uFlags = NIF_TIP;
    const std::wstring tip = L"Clipboard Exchange\n" + value;
    lstrcpynW(icon.szTip, tip.c_str(), ARRAYSIZE(icon.szTip));
    Shell_NotifyIconW(NIM_MODIFY, &icon);
}

void ShowNotification(const wchar_t* title, const std::wstring& message, DWORD flags) {
    if (!g_settings.notifications) return;
    NOTIFYICONDATAW icon = {};
    icon.cbSize = sizeof(icon);
    icon.hWnd = g_main;
    icon.uID = kTrayId;
    icon.uFlags = NIF_INFO;
    icon.dwInfoFlags = flags;
    lstrcpynW(icon.szInfoTitle, title, ARRAYSIZE(icon.szInfoTitle));
    lstrcpynW(icon.szInfo, message.c_str(), ARRAYSIZE(icon.szInfo));
    Shell_NotifyIconW(NIM_MODIFY, &icon);
}

void OpenRoomInBrowser() {
    const std::wstring room = WindowText(GetDlgItem(g_main, ID_ROOM_URL));
    std::wstring error;
    if (!ValidateRoomUrl(room, &error)) {
        SetStatus(room.empty() ? L"Сначала укажите ссылку комнаты" : error);
        return;
    }
    const HINSTANCE result = ShellExecuteW(g_main, L"open", room.c_str(), nullptr, nullptr, SW_SHOWNORMAL);
    if (reinterpret_cast<INT_PTR>(result) <= 32) SetStatus(L"Не удалось открыть комнату в браузере");
}

HWND AddControl(const wchar_t* type, const wchar_t* text, DWORD style, int x, int y,
                int width, int height, HWND parent, int id) {
    HWND control = CreateWindowExW(0, type, text, WS_CHILD | WS_VISIBLE | style, Px(x), Px(y),
                                   Px(width), Px(height), parent,
                                   reinterpret_cast<HMENU>(static_cast<INT_PTR>(id)),
                                   g_instance, nullptr);
    SendMessageW(control, WM_SETFONT, reinterpret_cast<WPARAM>(g_font), TRUE);
    return control;
}

LRESULT CALLBACK HotkeyEditSubclass(HWND window, UINT message, WPARAM wParam, LPARAM lParam,
                                    UINT_PTR, DWORD_PTR) {
    if (message == WM_GETDLGCODE) return DefSubclassProc(window, message, wParam, lParam) |
                                          DLGC_WANTALLKEYS;
    if (message == WM_SETFOCUS) {
        if (!g_hotkeyCaptureActive) {
            UnregisterAllHotkeys();
            g_hotkeyCaptureActive = true;
        }
        SetStatus(L"Нажмите новую комбинацию, например Ctrl+Shift+Alt+V.");
    }
    if (message == WM_KILLFOCUS) {
        const int nextId = lParam ? GetDlgCtrlID(reinterpret_cast<HWND>(lParam)) : 0;
        const bool nextIsHotkey = nextId >= ID_HOTKEY_CLIPBOARD && nextId <= ID_HOTKEY_HISTORY;
        if (g_hotkeyCaptureActive && !nextIsHotkey) {
            std::wstring error;
            g_hotkeyCaptureActive = false;
            if (!RegisterAllHotkeys(g_settings, &error)) SetStatus(error);
        }
    }
    if (message == WM_KEYDOWN || message == WM_SYSKEYDOWN) {
        const UINT key = static_cast<UINT>(wParam);
        if (key == VK_CONTROL || key == VK_SHIFT || key == VK_MENU || key == VK_LWIN || key == VK_RWIN) {
            SetStatus(L"Модификатор распознан. Не отпуская его, нажмите букву, цифру или F-клавишу.");
            return 0;
        }
        Hotkey hotkey = {MOD_NOREPEAT, key};
        if (GetKeyState(VK_CONTROL) & 0x8000) hotkey.modifiers |= MOD_CONTROL;
        if (GetKeyState(VK_SHIFT) & 0x8000) hotkey.modifiers |= MOD_SHIFT;
        if (GetKeyState(VK_MENU) & 0x8000) hotkey.modifiers |= MOD_ALT;
        if ((GetKeyState(VK_LWIN) | GetKeyState(VK_RWIN)) & 0x8000) hotkey.modifiers |= MOD_WIN;
        const bool supported = (key >= 'A' && key <= 'Z') || (key >= '0' && key <= '9') ||
                               (key >= VK_F1 && key <= VK_F24);
        if (!supported || !(hotkey.modifiers & (MOD_CONTROL | MOD_SHIFT | MOD_ALT | MOD_WIN))) {
            MessageBeep(MB_ICONWARNING);
            SetStatus(L"Нужны модификатор Ctrl, Shift, Alt или Win и буква, цифра или F-клавиша.");
            return 0;
        }
        SetWindowTextW(window, FormatHotkey(hotkey).c_str());
        SetStatus(L"Хоткей изменён. Нажмите «Сохранить», чтобы применить его.");
        return 0;
    }
    if (message == WM_CHAR || message == WM_SYSCHAR || message == WM_KEYUP || message == WM_SYSKEYUP) return 0;
    return DefSubclassProc(window, message, wParam, lParam);
}

void AddLabel(HWND parent, const wchar_t* text, int x, int y, int width) {
    AddControl(L"STATIC", text, SS_LEFT, x, y, width, 22, parent, -1);
}

bool SameHotkey(const Hotkey& left, const Hotkey& right) {
    constexpr UINT mask = MOD_ALT | MOD_CONTROL | MOD_SHIFT | MOD_WIN;
    return (left.modifiers & mask) == (right.modifiers & mask) && left.key == right.key;
}

void UnregisterAllHotkeys() {
    for (int id = HK_SEND_CLIPBOARD; id <= HK_SHOW_HISTORY; ++id) UnregisterHotKey(g_main, id);
}

bool RegisterAllHotkeys(const Settings& settings, std::wstring* error) {
    struct Registration { int id; const Hotkey* hotkey; const wchar_t* name; };
    const Registration registrations[] = {
        {HK_SEND_CLIPBOARD, &settings.sendClipboard, L"отправки буфера"},
        {HK_SEND_SELECTION, &settings.sendSelection, L"отправки выделения"},
        {HK_SHOW_LATEST, &settings.showLatest, L"вставки последнего сообщения"},
        {HK_SHOW_HISTORY, &settings.showHistory, L"истории"}
    };
    for (const Registration& value : registrations) {
        if (!RegisterHotKey(g_main, value.id, value.hotkey->modifiers, value.hotkey->key)) {
            for (int id = HK_SEND_CLIPBOARD; id <= value.id; ++id) UnregisterHotKey(g_main, id);
            if (error) *error = std::wstring(L"Хоткей для ") + value.name + L" уже занят";
            return false;
        }
    }
    return true;
}

void FillHotkeyControls(HWND window, const Settings& settings) {
    SetWindowTextW(GetDlgItem(window, ID_HOTKEY_CLIPBOARD), FormatHotkey(settings.sendClipboard).c_str());
    SetWindowTextW(GetDlgItem(window, ID_HOTKEY_SELECTION), FormatHotkey(settings.sendSelection).c_str());
    SetWindowTextW(GetDlgItem(window, ID_HOTKEY_LATEST), FormatHotkey(settings.showLatest).c_str());
    SetWindowTextW(GetDlgItem(window, ID_HOTKEY_HISTORY), FormatHotkey(settings.showHistory).c_str());
}

void FillSettingsControls(HWND window) {
    SetWindowTextW(GetDlgItem(window, ID_ROOM_URL), g_settings.roomUrl.c_str());
    FillHotkeyControls(window, g_settings);
    Button_SetCheck(GetDlgItem(window, ID_START_AT_LOGIN), g_settings.startAtLogin ? BST_CHECKED : BST_UNCHECKED);
    Button_SetCheck(GetDlgItem(window, ID_NOTIFICATIONS), g_settings.notifications ? BST_CHECKED : BST_UNCHECKED);
}

bool ReadSettingsControls(Settings* settings, std::wstring* error) {
    Settings next = g_settings;
    next.roomUrl = WindowText(GetDlgItem(g_main, ID_ROOM_URL));
    const struct Field { int id; Hotkey* destination; } fields[] = {
        {ID_HOTKEY_CLIPBOARD, &next.sendClipboard},
        {ID_HOTKEY_SELECTION, &next.sendSelection},
        {ID_HOTKEY_LATEST, &next.showLatest},
        {ID_HOTKEY_HISTORY, &next.showHistory}
    };
    for (const Field& field : fields) {
        if (!ParseHotkey(WindowText(GetDlgItem(g_main, field.id)), field.destination, error)) return false;
    }
    next.startAtLogin = Button_GetCheck(GetDlgItem(g_main, ID_START_AT_LOGIN)) == BST_CHECKED;
    next.notifications = Button_GetCheck(GetDlgItem(g_main, ID_NOTIFICATIONS)) == BST_CHECKED;
    const Hotkey values[] = {next.sendClipboard, next.sendSelection, next.showLatest, next.showHistory};
    for (size_t i = 0; i < 4; ++i) {
        for (size_t j = i + 1; j < 4; ++j) {
            if (SameHotkey(values[i], values[j])) {
                if (error) *error = L"Хоткеи разных действий не должны совпадать";
                return false;
            }
        }
    }
    *settings = next;
    return true;
}

void SaveFromWindow() {
    Settings next;
    std::wstring error;
    if (!ReadSettingsControls(&next, &error)) {
        MessageBoxW(g_main, error.c_str(), L"Clipboard Exchange", MB_OK | MB_ICONWARNING);
        return;
    }
    if (!ValidateRoomUrl(next.roomUrl, &error)) {
        MessageBoxW(g_main, error.c_str(), L"Clipboard Exchange", MB_OK | MB_ICONWARNING);
        return;
    }
    const Settings previous = g_settings;
    UnregisterAllHotkeys();
    if (!RegisterAllHotkeys(next, &error)) {
        RegisterAllHotkeys(previous, nullptr);
        MessageBoxW(g_main, error.c_str(), L"Clipboard Exchange", MB_OK | MB_ICONWARNING);
        return;
    }
    if (!SaveSettings(next, &error)) {
        UnregisterAllHotkeys();
        RegisterAllHotkeys(previous, nullptr);
        MessageBoxW(g_main, error.c_str(), L"Clipboard Exchange", MB_OK | MB_ICONERROR);
        return;
    }
    g_settings = next;
    InterlockedIncrement(&g_generation);
    InterlockedExchange(&g_eventGeneration, 0);
    g_realtimeUnsupported = false;
    g_history = LoadHistoryCache(g_settings.roomUrl);
    RefreshPickerList();
    SetStatus(L"Подключение к комнате…");
    StartNetworkTask(false);
    StartEventTask();
}

void AddTrayIcon() {
    NOTIFYICONDATAW icon = {};
    icon.cbSize = sizeof(icon);
    icon.hWnd = g_main;
    icon.uID = kTrayId;
    icon.uFlags = NIF_MESSAGE | NIF_ICON | NIF_TIP;
    icon.uCallbackMessage = kTrayMessage;
    icon.hIcon = LoadIconW(g_instance, MAKEINTRESOURCEW(1));
    lstrcpynW(icon.szTip, L"Clipboard Exchange", ARRAYSIZE(icon.szTip));
    Shell_NotifyIconW(NIM_ADD, &icon);
    icon.uVersion = NOTIFYICON_VERSION_4;
    Shell_NotifyIconW(NIM_SETVERSION, &icon);
}

void RemoveTrayIcon() {
    NOTIFYICONDATAW icon = {};
    icon.cbSize = sizeof(icon);
    icon.hWnd = g_main;
    icon.uID = kTrayId;
    Shell_NotifyIconW(NIM_DELETE, &icon);
}

void ShowTrayMenu() {
    POINT point;
    GetCursorPos(&point);
    HMENU menu = CreatePopupMenu();
    AppendMenuW(menu, MF_STRING, ID_TRAY_OPEN, L"Настройки");
    AppendMenuW(menu, MF_STRING, ID_TRAY_BROWSER, L"Открыть комнату в браузере");
    AppendMenuW(menu, MF_SEPARATOR, 0, nullptr);
    AppendMenuW(menu, MF_STRING, ID_TRAY_EXIT, L"Выход");
    SetForegroundWindow(g_main);
    TrackPopupMenu(menu, TPM_RIGHTBUTTON | TPM_BOTTOMALIGN | TPM_LEFTALIGN,
                   point.x, point.y, 0, g_main, nullptr);
    DestroyMenu(menu);
}

void HideMessagePreview() {
    if (g_picker) KillTimer(g_picker, kPreviewHoverTimer);
    if (g_preview) ShowWindow(g_preview, SW_HIDE);
    g_previewActivated = false;
    g_previewHistoryIndex = static_cast<size_t>(-1);
}

void ShowMessagePreview(size_t historyIndex, bool activate) {
    if (!g_preview || !g_previewEdit || historyIndex >= g_pickerHistory.size()) return;
    const std::wstring displayText = WindowsEditText(g_pickerHistory[historyIndex]);
    SetWindowTextW(g_previewEdit, displayText.c_str());
    SendMessageW(g_previewEdit, EM_SETSEL, 0, 0);
    SendMessageW(g_previewEdit, EM_SCROLLCARET, 0, 0);

    RECT pickerRect = {}, work = {};
    GetWindowRect(g_picker, &pickerRect);
    SystemParametersInfoW(SPI_GETWORKAREA, 0, &work, 0);
    const int width = Px(600);
    const int lines = static_cast<int>(MessageLines(g_pickerHistory[historyIndex]).size());
    const int height = std::min(Px(420), std::max(Px(160), Px(58 + lines * 20)));
    int x = pickerRect.left - width - Px(8);
    if (x < work.left) x = pickerRect.right + Px(8);
    x = std::max(static_cast<int>(work.left),
                 std::min(x, static_cast<int>(work.right) - width));
    int y = std::max(static_cast<int>(work.top),
                     std::min(static_cast<int>(pickerRect.top),
                              static_cast<int>(work.bottom) - height));
    SetWindowPos(g_preview, HWND_TOPMOST, x, y, width, height,
                 SWP_SHOWWINDOW | (activate ? 0 : SWP_NOACTIVATE));
    g_previewHistoryIndex = historyIndex;
    g_previewActivated = activate;
    if (activate) {
        KillTimer(g_picker, kPreviewHoverTimer);
        SetForegroundWindow(g_preview);
        SetFocus(g_previewEdit);
    } else SetTimer(g_picker, kPreviewHoverTimer, 250, nullptr);
}

void ShowSelectedMessagePreview() {
    const LRESULT selected = SendMessageW(g_pickerList, LB_GETCURSEL, 0, 0);
    if (selected == LB_ERR) return;
    const LRESULT itemData = SendMessageW(g_pickerList, LB_GETITEMDATA, selected, 0);
    if (itemData != LB_ERR) ShowMessagePreview(static_cast<size_t>(itemData), true);
}

LRESULT CALLBACK PickerListSubclass(HWND window, UINT message, WPARAM wParam, LPARAM lParam,
                                    UINT_PTR, DWORD_PTR) {
    if (message == WM_MOUSEMOVE) {
        TRACKMOUSEEVENT tracking = {sizeof(tracking), TME_LEAVE, window, 0};
        TrackMouseEvent(&tracking);
        const LRESULT hit = SendMessageW(window, LB_ITEMFROMPOINT, 0, lParam);
        const bool outside = HIWORD(hit) != 0;
        const int item = LOWORD(hit);
        if (!outside && item >= 0) {
            const LRESULT itemData = SendMessageW(window, LB_GETITEMDATA, item, 0);
            const size_t historyIndex = static_cast<size_t>(itemData);
            if (itemData != LB_ERR && historyIndex < g_pickerHistory.size() &&
                MessageIsTruncated(g_pickerHistory[historyIndex])) {
                if (!g_previewActivated && g_previewHistoryIndex != historyIndex) {
                    ShowMessagePreview(historyIndex, false);
                }
            } else if (!g_previewActivated) HideMessagePreview();
        }
    }
    return DefSubclassProc(window, message, wParam, lParam);
}

void RefreshPickerList() {
    HideMessagePreview();
    g_pickerHistory = g_history;
    SendMessageW(g_pickerList, WM_SETREDRAW, FALSE, 0);
    SendMessageW(g_pickerList, LB_RESETCONTENT, 0, 0);
    if (g_pickerHistory.empty()) {
        SendMessageW(g_pickerList, LB_ADDSTRING, 0, static_cast<LPARAM>(-1));
    } else {
        for (size_t index = 0; index < g_pickerHistory.size(); ++index) {
            SendMessageW(g_pickerList, LB_ADDSTRING, 0, static_cast<LPARAM>(index));
        }
        SendMessageW(g_pickerList, LB_SETCURSEL, 0, 0);
    }
    SendMessageW(g_pickerList, WM_SETREDRAW, TRUE, 0);
    InvalidateRect(g_pickerList, nullptr, TRUE);
    g_pickerDirty = false;
}

void PositionPicker() {
    RECT work;
    SystemParametersInfoW(SPI_GETWORKAREA, 0, &work, 0);
    const int width = Px(560);
    const int height = Px(330);
    POINT cursor;
    GetCursorPos(&cursor);
    int x = cursor.x - width / 2;
    int y = cursor.y - Px(36);
    const int left = static_cast<int>(work.left) + Px(8);
    const int top = static_cast<int>(work.top) + Px(8);
    const int right = static_cast<int>(work.right) - width - Px(8);
    const int bottom = static_cast<int>(work.bottom) - height - Px(8);
    x = std::max(left, std::min(x, right));
    y = std::max(top, std::min(y, bottom));
    SetWindowPos(g_picker, HWND_TOPMOST, x, y, width, height, SWP_NOACTIVATE);
}

void ShowPicker() {
    LARGE_INTEGER frequency, started, shown;
    QueryPerformanceFrequency(&frequency);
    QueryPerformanceCounter(&started);
    g_pickerTarget = GetForegroundWindow();
    PositionPicker();
    ShowWindow(g_picker, SW_SHOWNORMAL);
    SetForegroundWindow(g_picker);
    SetFocus(g_pickerList);
    QueryPerformanceCounter(&shown);
    const double milliseconds = 1000.0 * (shown.QuadPart - started.QuadPart) / frequency.QuadPart;
    std::wostringstream trace;
    trace << L"Clipboard Exchange picker shown in " << milliseconds << L" ms\n";
    OutputDebugStringW(trace.str().c_str());
}

void QueuePickerInsertion() {
    if (g_pickerHistory.empty()) {
        MessageBeep(MB_ICONINFORMATION);
        return;
    }
    const LRESULT selected = SendMessageW(g_pickerList, LB_GETCURSEL, 0, 0);
    if (selected == LB_ERR) return;
    const LRESULT itemData = SendMessageW(g_pickerList, LB_GETITEMDATA, selected, 0);
    if (itemData == LB_ERR) return;
    const size_t historyIndex = static_cast<size_t>(itemData);
    if (historyIndex >= g_pickerHistory.size()) return;
    g_pendingInsertion = g_pickerHistory[historyIndex];
    g_pendingTarget = g_pickerTarget;
    ShowWindow(g_picker, SW_HIDE);
    if (g_pendingTarget) SetForegroundWindow(g_pendingTarget);
    SetTimer(g_main, kInsertTimer, 15, nullptr);
}

void InsertLatest() {
    if (g_history.empty()) {
        SetStatus(L"В комнате пока нет сообщений");
        MessageBeep(MB_ICONINFORMATION);
        return;
    }
    g_pendingInsertion = g_history.front();
    g_pendingTarget = GetForegroundWindow();
    SetTimer(g_main, kInsertTimer, 15, nullptr);
}

bool HotkeyModifiersDown() {
    return (GetAsyncKeyState(VK_CONTROL) & 0x8000) || (GetAsyncKeyState(VK_SHIFT) & 0x8000) ||
           (GetAsyncKeyState(VK_MENU) & 0x8000) || (GetAsyncKeyState(VK_LWIN) & 0x8000) ||
           (GetAsyncKeyState(VK_RWIN) & 0x8000);
}

void HandleHotkey(WPARAM id) {
    if (id == HK_SHOW_HISTORY) {
        ShowPicker();
    } else if (id == HK_SHOW_LATEST) {
        InsertLatest();
    } else if (id == HK_SEND_CLIPBOARD) {
        std::wstring text, error;
        if (ReadClipboardText(g_main, &text, &error)) {
            SetStatus(L"Отправка текста из буфера…");
            StartNetworkTask(true, text);
        } else SetStatus(error);
    } else if (id == HK_SEND_SELECTION) {
        SetStatus(L"Получение и отправка выделенного текста…");
        StartNetworkTask(true, {}, true);
    }
}

LRESULT CALLBACK PreviewProcedure(HWND window, UINT message, WPARAM wParam, LPARAM lParam) {
    switch (message) {
    case WM_CREATE:
        g_previewEdit = AddControl(L"EDIT", L"", WS_TABSTOP | WS_BORDER | ES_MULTILINE |
                                   ES_READONLY | ES_AUTOVSCROLL | WS_VSCROLL,
                                   8, 8, 560, 300, window, -1);
        return 0;
    case WM_SIZE:
        MoveWindow(g_previewEdit, Px(8), Px(8), LOWORD(lParam) - Px(16),
                   HIWORD(lParam) - Px(16), TRUE);
        return 0;
    case WM_ACTIVATE:
        if (LOWORD(wParam) != WA_INACTIVE) {
            g_previewActivated = true;
            KillTimer(g_picker, kPreviewHoverTimer);
        } else if (reinterpret_cast<HWND>(lParam) != g_picker) {
            HideMessagePreview();
        } else {
            g_previewActivated = false;
            SetTimer(g_picker, kPreviewHoverTimer, 250, nullptr);
        }
        return 0;
    case WM_CLOSE:
        HideMessagePreview();
        if (IsWindowVisible(g_picker)) {
            SetForegroundWindow(g_picker);
            SetFocus(g_pickerList);
        }
        return 0;
    }
    return DefWindowProcW(window, message, wParam, lParam);
}

LRESULT CALLBACK PickerProcedure(HWND window, UINT message, WPARAM wParam, LPARAM lParam) {
    switch (message) {
    case WM_CREATE:
        g_pickerList = AddControl(L"LISTBOX", L"", LBS_NOTIFY | LBS_NOINTEGRALHEIGHT |
                                  LBS_OWNERDRAWVARIABLE | WS_VSCROLL | WS_TABSTOP | WS_BORDER,
                                  12, 42, 520, 250, window, ID_PICKER_LIST);
        SetWindowSubclass(g_pickerList, PickerListSubclass, 1, 0);
        AddControl(L"STATIC", L"Последние сообщения  ·  Enter — вставить  ·  F2 — просмотр  ·  Esc — закрыть",
                   SS_LEFT, 14, 13, 520, 22, window, -1);
        g_preview = CreateWindowExW(WS_EX_TOOLWINDOW | WS_EX_TOPMOST, kPreviewClass,
                                    L"Clipboard Exchange — полное сообщение",
                                    WS_POPUP | WS_CAPTION | WS_THICKFRAME,
                                    0, 0, Px(600), Px(300), window, nullptr, g_instance, nullptr);
        return 0;
    case WM_SIZE:
        MoveWindow(g_pickerList, Px(12), Px(42), LOWORD(lParam) - Px(24),
                   HIWORD(lParam) - Px(54), TRUE);
        return 0;
    case WM_COMMAND:
        if (LOWORD(wParam) == ID_PICKER_LIST && HIWORD(wParam) == LBN_DBLCLK) QueuePickerInsertion();
        return 0;
    case WM_MEASUREITEM: {
        MEASUREITEMSTRUCT* measure = reinterpret_cast<MEASUREITEMSTRUCT*>(lParam);
        if (measure && measure->CtlID == ID_PICKER_LIST) {
            size_t lineCount = 1;
            const size_t historyIndex = static_cast<size_t>(measure->itemData);
            if (historyIndex < g_pickerHistory.size()) {
                lineCount = VisibleMessageLineCount(g_pickerHistory[historyIndex]);
            }
            measure->itemHeight = static_cast<UINT>(Px(11) + Px(19) * lineCount);
            return TRUE;
        }
        break;
    }
    case WM_DRAWITEM: {
        DRAWITEMSTRUCT* draw = reinterpret_cast<DRAWITEMSTRUCT*>(lParam);
        if (!draw || draw->CtlID != ID_PICKER_LIST || draw->itemID == static_cast<UINT>(-1)) break;
        const bool selected = (draw->itemState & ODS_SELECTED) != 0;
        FillRect(draw->hDC, &draw->rcItem,
                 GetSysColorBrush(selected ? COLOR_HIGHLIGHT : COLOR_WINDOW));
        SetBkMode(draw->hDC, TRANSPARENT);
        SetTextColor(draw->hDC, GetSysColor(selected ? COLOR_HIGHLIGHTTEXT : COLOR_WINDOWTEXT));
        HFONT previousFont = static_cast<HFONT>(SelectObject(draw->hDC, g_font));
        const size_t historyIndex = static_cast<size_t>(draw->itemData);
        std::vector<std::wstring> lines;
        if (historyIndex < g_pickerHistory.size()) lines = MessageLines(g_pickerHistory[historyIndex]);
        else lines = {L"Пока нет сообщений"};
        const bool truncated = lines.size() > 6;
        const size_t textLines = truncated ? 5 : lines.size();
        RECT lineRect = draw->rcItem;
        lineRect.left += Px(8);
        lineRect.right -= Px(8);
        lineRect.top += Px(5);
        for (size_t index = 0; index < textLines; ++index) {
            RECT current = lineRect;
            current.top += static_cast<LONG>(Px(19) * index);
            current.bottom = current.top + Px(19);
            DrawTextW(draw->hDC, const_cast<wchar_t*>(lines[index].c_str()), -1, &current,
                      DT_SINGLELINE | DT_NOPREFIX | DT_END_ELLIPSIS | DT_EXPANDTABS);
        }
        if (truncated) {
            RECT ellipsis = lineRect;
            ellipsis.top += Px(19) * 5;
            ellipsis.bottom = ellipsis.top + Px(19);
            DrawTextW(draw->hDC, const_cast<wchar_t*>(L"…"), -1, &ellipsis,
                      DT_SINGLELINE | DT_NOPREFIX);
        }
        SelectObject(draw->hDC, previousFont);
        RECT separator = draw->rcItem;
        separator.left += Px(2);
        separator.right -= Px(2);
        separator.top = separator.bottom - Px(1);
        FillRect(draw->hDC, &separator, GetSysColorBrush(COLOR_3DLIGHT));
        if (draw->itemState & ODS_FOCUS) {
            RECT focus = draw->rcItem;
            focus.bottom -= Px(2);
            DrawFocusRect(draw->hDC, &focus);
        }
        return TRUE;
    }
    case WM_ACTIVATE:
        if (LOWORD(wParam) == WA_INACTIVE && reinterpret_cast<HWND>(lParam) != g_preview) {
            HideMessagePreview();
            ShowWindow(window, SW_HIDE);
            if (g_pickerDirty) RefreshPickerList();
        }
        return 0;
    case WM_KEYDOWN:
        if (wParam == VK_ESCAPE) ShowWindow(window, SW_HIDE);
        else if (wParam == VK_RETURN) QueuePickerInsertion();
        else if (wParam == VK_F2) ShowSelectedMessagePreview();
        return 0;
    case WM_TIMER:
        if (wParam == kPreviewHoverTimer && !g_previewActivated && IsWindowVisible(g_preview)) {
            POINT cursor = {};
            RECT listRect = {}, previewRect = {};
            GetCursorPos(&cursor);
            GetWindowRect(g_pickerList, &listRect);
            GetWindowRect(g_preview, &previewRect);
            if (!PtInRect(&listRect, cursor) && !PtInRect(&previewRect, cursor)) {
                HideMessagePreview();
            }
            return 0;
        }
        break;
    case WM_CLOSE:
        HideMessagePreview();
        ShowWindow(window, SW_HIDE);
        return 0;
    }
    return DefWindowProcW(window, message, wParam, lParam);
}

LRESULT CALLBACK MainProcedure(HWND window, UINT message, WPARAM wParam, LPARAM lParam) {
    switch (message) {
    case WM_CREATE: {
        AddLabel(window, L"Ссылка комнаты", 24, 22, 180);
        AddControl(L"EDIT", L"", WS_TABSTOP | WS_BORDER | ES_AUTOHSCROLL,
                   24, 46, 592, 28, window, ID_ROOM_URL);
        AddLabel(window, L"Глобальные хоткеи", 24, 91, 220);
        AddLabel(window, L"Кликните по полю и нажмите новую комбинацию", 24, 113, 592);
        const wchar_t* labels[] = {L"Отправить буфер", L"Отправить выделение",
                                   L"Вставить последнее", L"Показать историю"};
        const int ids[] = {ID_HOTKEY_CLIPBOARD, ID_HOTKEY_SELECTION,
                           ID_HOTKEY_LATEST, ID_HOTKEY_HISTORY};
        for (int index = 0; index < 4; ++index) {
            const int y = 140 + index * 39;
            AddLabel(window, labels[index], 24, y + 4, 235);
            HWND hotkeyEdit = AddControl(L"EDIT", L"", WS_TABSTOP | WS_BORDER | ES_AUTOHSCROLL |
                                         ES_READONLY, 270, y, 346, 28, window, ids[index]);
            SetWindowSubclass(hotkeyEdit, HotkeyEditSubclass, 1, 0);
        }
        AddControl(L"BUTTON", L"Сохранить", WS_TABSTOP | BS_DEFPUSHBUTTON,
                   476, 376, 140, 34, window, ID_SAVE);
        AddControl(L"BUTTON", L"Открыть в браузере", WS_TABSTOP,
                   270, 376, 190, 34, window, ID_OPEN_BROWSER);
        AddControl(L"BUTTON", L"Хоткеи по умолчанию", WS_TABSTOP,
                   24, 376, 230, 34, window, ID_RESET_HOTKEYS);
        AddControl(L"BUTTON", L"Запускать при входе в Windows", WS_TABSTOP | BS_AUTOCHECKBOX,
                   24, 310, 330, 24, window, ID_START_AT_LOGIN);
        AddControl(L"BUTTON", L"Показывать уведомления об отправке", WS_TABSTOP | BS_AUTOCHECKBOX,
                   24, 342, 360, 24, window, ID_NOTIFICATIONS);
        AddControl(L"STATIC", L"", SS_LEFT, 24, 431, 592, 48, window, ID_STATUS);
        FillSettingsControls(window);
        return 0;
    }
    case WM_COMMAND:
        if (LOWORD(wParam) == ID_SAVE) SaveFromWindow();
        else if (LOWORD(wParam) == ID_OPEN_BROWSER || LOWORD(wParam) == ID_TRAY_BROWSER) OpenRoomInBrowser();
        else if (LOWORD(wParam) == ID_RESET_HOTKEYS) {
            FillHotkeyControls(window, DefaultSettings());
            SetStatus(L"Восстановлены хоткеи по умолчанию. Нажмите «Сохранить».");
        }
        else if (LOWORD(wParam) == ID_TRAY_OPEN) {
            ShowWindow(window, SW_SHOWNORMAL);
            SetForegroundWindow(window);
        } else if (LOWORD(wParam) == ID_TRAY_EXIT) {
            g_exiting = true;
            DestroyWindow(window);
        }
        return 0;
    case WM_COPYDATA: {
        const COPYDATASTRUCT* data = reinterpret_cast<const COPYDATASTRUCT*>(lParam);
        if (data && data->lpData && data->cbData >= sizeof(wchar_t) &&
            data->cbData <= 32768 * sizeof(wchar_t) && data->cbData % sizeof(wchar_t) == 0 &&
            static_cast<const wchar_t*>(data->lpData)[data->cbData / sizeof(wchar_t) - 1] == L'\0') {
            return PresentIncomingRoom(window, static_cast<const wchar_t*>(data->lpData)) ? TRUE : FALSE;
        }
        return FALSE;
    }
    case WM_HOTKEY:
        HandleHotkey(wParam);
        return 0;
    case WM_TIMER:
        if (wParam == kInsertTimer) {
            if (HotkeyModifiersDown()) {
                SetTimer(window, kInsertTimer, 10, nullptr);
                return 0;
            }
            KillTimer(window, kInsertTimer);
            std::wstring error;
            if (!InsertUnicodeText(g_pendingInsertion, &error)) SetStatus(error);
            g_pendingInsertion.clear();
            g_pendingTarget = nullptr;
        } else if (wParam == kRefreshTimer) {
            StartEventTask();
            if (g_realtimeUnsupported || GetTickCount64() - g_lastSnapshotTick >= 30000) {
                StartNetworkTask(false);
            }
        }
        return 0;
    case kNetworkMessage: {
        NetworkResult* result = reinterpret_cast<NetworkResult*>(lParam);
        if (result->generation == g_generation) {
            if (result->ok) {
                const bool encrypted = result->snapshot.encrypted;
                const bool canWrite = result->snapshot.canWrite;
                g_history.swap(result->snapshot.messages);
                if (g_history.size() > 50) g_history.resize(50);
                g_lastSnapshotTick = GetTickCount64();
                if (IsWindowVisible(g_picker)) g_pickerDirty = true;
                else RefreshPickerList();
                if (result->send) {
                    SetStatus(L"Сообщение отправлено");
                    ShowNotification(L"Clipboard Exchange", L"Сообщение отправлено", NIIF_INFO);
                }
                else {
                    std::wstring status = L"Комната подключена · ";
                    if (encrypted) status += L"E2EE · ";
                    status += canWrite ? L"R/W" : L"R/O";
                    status += L" · сообщений: " + std::to_wstring(g_history.size());
                    SetStatus(status);
                }
            } else {
                SetStatus(result->error);
                if (result->send) ShowNotification(L"Ошибка отправки", result->error, NIIF_ERROR);
            }
        }
        delete result;
        return 0;
    }
    case kRoomEventMessage: {
        const LONG generation = static_cast<LONG>(lParam);
        if (generation == g_generation) {
            InterlockedCompareExchange(&g_eventGeneration, 0, generation);
            const RoomEventResult result = static_cast<RoomEventResult>(wParam);
            if (result == RoomEventResult::Unsupported) g_realtimeUnsupported = true;
            else if (result == RoomEventResult::Refresh) StartNetworkTask(false);
        }
        return 0;
    }
    case kTrayMessage:
        if (LOWORD(lParam) == WM_LBUTTONUP) {
            ShowWindow(window, SW_SHOWNORMAL);
            SetForegroundWindow(window);
        } else if (LOWORD(lParam) == WM_RBUTTONUP || LOWORD(lParam) == WM_CONTEXTMENU) {
            ShowTrayMenu();
        }
        return 0;
    case WM_CLOSE:
        if (!g_exiting) {
            ShowWindow(window, SW_HIDE);
            return 0;
        }
        break;
    case WM_DESTROY:
        g_exiting = true;
        InterlockedIncrement(&g_generation);
        KillTimer(window, kInsertTimer);
        KillTimer(window, kRefreshTimer);
        UnregisterAllHotkeys();
        RemoveTrayIcon();
        PostQuitMessage(0);
        return 0;
    }
    return DefWindowProcW(window, message, wParam, lParam);
}

bool RegisterClasses() {
    WNDCLASSEXW mainClass = {};
    mainClass.cbSize = sizeof(mainClass);
    mainClass.hInstance = g_instance;
    mainClass.lpfnWndProc = MainProcedure;
    mainClass.lpszClassName = kMainClass;
    mainClass.hCursor = LoadCursorW(nullptr, IDC_ARROW);
    mainClass.hIcon = LoadIconW(g_instance, MAKEINTRESOURCEW(1));
    mainClass.hIconSm = static_cast<HICON>(LoadImageW(g_instance, MAKEINTRESOURCEW(1), IMAGE_ICON,
                                                      GetSystemMetrics(SM_CXSMICON),
                                                      GetSystemMetrics(SM_CYSMICON), LR_DEFAULTCOLOR));
    mainClass.hbrBackground = reinterpret_cast<HBRUSH>(COLOR_WINDOW + 1);
    if (!RegisterClassExW(&mainClass)) return false;

    WNDCLASSEXW pickerClass = mainClass;
    pickerClass.lpfnWndProc = PickerProcedure;
    pickerClass.lpszClassName = kPickerClass;
    pickerClass.hbrBackground = reinterpret_cast<HBRUSH>(COLOR_WINDOW + 1);
    if (!RegisterClassExW(&pickerClass)) return false;

    WNDCLASSEXW previewClass = mainClass;
    previewClass.lpfnWndProc = PreviewProcedure;
    previewClass.lpszClassName = kPreviewClass;
    previewClass.hbrBackground = reinterpret_cast<HBRUSH>(COLOR_WINDOW + 1);
    return RegisterClassExW(&previewClass) != 0;
}
}

int WINAPI wWinMain(HINSTANCE instance, HINSTANCE, PWSTR commandLine, int showCommand) {
    g_instance = instance;
    g_singleInstance = CreateMutexW(nullptr, FALSE, L"Local\\ClipboardExchange.Win32.Singleton.v1");
    if (!g_singleInstance) return 1;
    if (GetLastError() == ERROR_ALREADY_EXISTS) {
        HWND existing = FindWindowW(kMainClass, nullptr);
        if (existing) {
            if (commandLine && *commandLine) {
                COPYDATASTRUCT data = {};
                data.cbData = static_cast<DWORD>((wcslen(commandLine) + 1) * sizeof(wchar_t));
                data.lpData = commandLine;
                SendMessageTimeoutW(existing, WM_COPYDATA, 0, reinterpret_cast<LPARAM>(&data),
                                    SMTO_ABORTIFHUNG, 2000, nullptr);
            }
            ShowWindow(existing, SW_SHOWNORMAL);
            SetForegroundWindow(existing);
        }
        CloseHandle(g_singleInstance);
        return 0;
    }
    const HRESULT comResult = CoInitializeEx(nullptr, COINIT_APARTMENTTHREADED);
    typedef BOOL (WINAPI *SetProcessDPIAwareFunction)();
    HMODULE user32 = GetModuleHandleW(L"user32.dll");
    SetProcessDPIAwareFunction setDpiAware = user32
        ? reinterpret_cast<SetProcessDPIAwareFunction>(GetProcAddress(user32, "SetProcessDPIAware"))
        : nullptr;
    if (setDpiAware) setDpiAware();
    HDC screen = GetDC(nullptr);
    if (screen) {
        g_dpi = GetDeviceCaps(screen, LOGPIXELSX);
        ReleaseDC(nullptr, screen);
    }
    INITCOMMONCONTROLSEX controls = {sizeof(controls), ICC_STANDARD_CLASSES};
    InitCommonControlsEx(&controls);
    g_font = CreateFontW(-Px(16), 0, 0, 0, FW_NORMAL, FALSE, FALSE, FALSE, DEFAULT_CHARSET,
                         OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS, CLEARTYPE_QUALITY,
                         DEFAULT_PITCH | FF_DONTCARE, L"Segoe UI");
    if (!RegisterClasses()) return 1;

    g_settings = LoadSettings();
    g_history = LoadHistoryCache(g_settings.roomUrl);
    g_main = CreateWindowExW(0, kMainClass, L"Clipboard Exchange — настройки",
                             WS_OVERLAPPED | WS_CAPTION | WS_SYSMENU | WS_MINIMIZEBOX,
                             CW_USEDEFAULT, CW_USEDEFAULT, Px(660), Px(535), nullptr, nullptr,
                             instance, nullptr);
    if (!g_main) return 2;
    g_picker = CreateWindowExW(WS_EX_TOOLWINDOW | WS_EX_TOPMOST, kPickerClass,
                               L"Clipboard Exchange — последние сообщения",
                               WS_POPUP | WS_CAPTION | WS_THICKFRAME, 0, 0, Px(560), Px(330),
                               nullptr, nullptr, instance, nullptr);
    if (!g_picker) return 3;
    RefreshPickerList();
    AddTrayIcon();
    SetTimer(g_main, kRefreshTimer, 2000, nullptr);

    std::wstring error;
    if (!RegisterAllHotkeys(g_settings, &error)) SetStatus(error);
    else if (g_settings.roomUrl.empty()) SetStatus(L"Введите ссылку комнаты и сохраните настройки.");
    else {
        SetStatus(L"Подключение к комнате…");
        StartNetworkTask(false);
        StartEventTask();
    }
    const std::wstring incoming = ExtractRoomArgument(commandLine ? commandLine : L"");
    const bool background = commandLine && wcsstr(commandLine, L"--background") && incoming.empty();
    ShowWindow(g_main, background ? SW_HIDE : showCommand);
    if (!incoming.empty()) PresentIncomingRoom(g_main, commandLine);
    UpdateWindow(g_main);

    MSG message;
    while (GetMessageW(&message, nullptr, 0, 0) > 0) {
        if (g_preview && IsWindowVisible(g_preview) &&
            (message.hwnd == g_preview || IsChild(g_preview, message.hwnd)) &&
            message.message == WM_KEYDOWN && message.wParam == VK_ESCAPE) {
            SendMessageW(g_preview, WM_CLOSE, 0, 0);
            continue;
        }
        if (g_picker && IsWindowVisible(g_picker) &&
            (message.hwnd == g_picker || IsChild(g_picker, message.hwnd)) &&
            message.message == WM_KEYDOWN) {
            if (message.wParam == VK_ESCAPE) {
                ShowWindow(g_picker, SW_HIDE);
                continue;
            }
            if (message.wParam == VK_RETURN) {
                QueuePickerInsertion();
                continue;
            }
            if (message.wParam == VK_F2) {
                ShowSelectedMessagePreview();
                continue;
            }
        }
        if (g_picker && IsDialogMessageW(g_picker, &message)) continue;
        TranslateMessage(&message);
        DispatchMessageW(&message);
    }
    if (g_font) DeleteObject(g_font);
    if (SUCCEEDED(comResult)) CoUninitialize();
    CloseHandle(g_singleInstance);
    return static_cast<int>(message.wParam);
}
