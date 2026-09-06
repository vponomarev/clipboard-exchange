#include "room_client.h"

#include <iostream>

namespace {
int failures = 0;

void Expect(const wchar_t* url, bool valid) {
    std::wstring error;
    if (ValidateRoomUrl(url, &error) != valid) {
        std::wcerr << L"unexpected URL result: " << url << L" (" << error << L")\n";
        ++failures;
    }
}
}

int main() {
    Expect(L"http://localhost:8080/r/home", true);
    Expect(L"https://example.test/r/a-b_9/#write=cw1_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", true);
    Expect(L"https://example.test/r/e2ee#key=ce1_AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8", true);
    Expect(L"ftp://example.test/r/home", false);
    Expect(L"https://example.test/", false);
    Expect(L"https://user:password@example.test/r/home", false);
    Expect(L"https://example.test/r/a/b", false);
    Expect(L"https://example.test/r/кириллица", false);
    Expect(L"https://example.test/r/home#key=ce1_short", false);
    Expect(L"https://example.test/r/home#write=cw1_short", false);
    Expect(L"https://example.test/r/home#write=cw1_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa%0D", false);
    if (!failures) std::cout << "room URL boundary tests passed\n";
    return failures ? 1 : 0;
}
