// Include the private wire codec to test its exact JSON representation as well
// as round trips. This target does not separately compile room_client.cpp.
#include "../src/room_client.cpp"
#include <iostream>

int main() {
    const std::wstring text = L"Привет \U0001F600 \U0001D11E 世界\n\t\"\\";
    const std::string expected = u8"Привет \U0001F600 \U0001D11E 世界\\n\\t\\\"\\\\";
    const std::string encoded = JsonEscape(text);
    if (encoded != expected || DecodeString("\"" + encoded + "\"", 0, nullptr) != text) {
        std::cerr << "Unicode JSON round trip changed text\n";
        return 1;
    }
    return 0;
}
