#include "deep_link.h"

#include <iostream>

int main() {
    int failures = 0;
    const auto expect = [&failures](const wchar_t* input, const wchar_t* expected) {
        const std::wstring actual = ExtractRoomArgument(input);
        if (actual != expected) {
            std::wcerr << L"deep link mismatch: input=" << input << L" actual=" << actual
                       << L" expected=" << expected << L"\n";
            ++failures;
        }
    };
    expect(L"https://example.test/r/direct-room", L"https://example.test/r/direct-room");
    expect(L"\"https://example.test/r/quoted-room#write=cw1_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"",
           L"https://example.test/r/quoted-room#write=cw1_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa");
    expect(L"clipboard-exchange://connect?url=https%3A%2F%2Fexample.test%2Fr%2Frelease-room",
           L"https://example.test/r/release-room");
    expect(L"clipboard-exchange://connect?url=https%3A%2F%2Fexample.test%2Fr%2F%D1%82%D0%B5%D1%81%D1%82",
           L"");
    if (!failures) std::cout << "deep link parsing tests passed\n";
    return failures ? 1 : 0;
}
