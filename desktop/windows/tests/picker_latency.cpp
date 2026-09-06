#include <windows.h>

#include <algorithm>
#include <iostream>

int main() {
    HWND main = FindWindowW(L"ClipboardExchangeMainWindow", nullptr);
    HWND picker = FindWindowW(L"ClipboardExchangePickerWindow", nullptr);
    if (!main || !picker) {
        std::cerr << "native client is not running\n";
        return 2;
    }
    LARGE_INTEGER frequency;
    QueryPerformanceFrequency(&frequency);
    double maximum = 0;
    for (int iteration = 0; iteration < 20; ++iteration) {
        LARGE_INTEGER started, finished;
        QueryPerformanceCounter(&started);
        PostMessageW(main, WM_HOTKEY, 4, 0);
        const ULONGLONG deadline = GetTickCount64() + 1000;
        while (!IsWindowVisible(picker) && GetTickCount64() < deadline) Sleep(1);
        QueryPerformanceCounter(&finished);
        if (!IsWindowVisible(picker)) {
            std::cerr << "picker did not become visible\n";
            return 1;
        }
        maximum = std::max(maximum, 1000.0 * (finished.QuadPart - started.QuadPart) / frequency.QuadPart);
        PostMessageW(picker, WM_CLOSE, 0, 0);
        while (IsWindowVisible(picker) && GetTickCount64() < deadline + 1000) Sleep(1);
    }
    std::cout << "picker max latency over 20 runs: " << maximum << " ms\n";
    return maximum <= 250.0 ? 0 : 1;
}
