#include "crypto.h"

#include <iostream>
#include <string>
#include <vector>

namespace {
std::vector<unsigned char> Bytes(const std::string& value) {
    return std::vector<unsigned char>(value.begin(), value.end());
}
}

int main() {
    std::vector<unsigned char> key(32);
    for (size_t index = 0; index < key.size(); ++index) key[index] = static_cast<unsigned char>(index);
    std::vector<unsigned char> iv, expected;
    if (!Base64UrlDecode(L"AAECAwQFBgcICQoL", &iv) ||
        !Base64UrlDecode(L"L2e6d6rlwhuNQZeLsel4bdiaIdDEtuOJ3wUwgkxN3fU", &expected)) {
        std::cerr << "base64url decode failed\n";
        return 1;
    }
    std::vector<unsigned char> plaintext(16, 0);
    const char hello[] = "hello";
    for (size_t index = 0; index < 5; ++index) plaintext[index] = hello[index];
    const std::vector<unsigned char> aad = Bytes("clipboard-exchange:file:v1:test-room:123e4567-e89b-12d3-a456-426614174000:0:16");
    std::vector<unsigned char> encrypted;
    if (!Aes256GcmEncrypt(key, iv, aad, plaintext, &encrypted) || encrypted != expected) {
        std::cerr << "AES-GCM does not match protocol vector\n";
        return 1;
    }
    std::vector<unsigned char> decrypted;
    if (!Aes256GcmDecrypt(key, iv, aad, encrypted, &decrypted) || decrypted != plaintext) {
        std::cerr << "AES-GCM decrypt failed\n";
        return 1;
    }
    std::vector<unsigned char> digest;
    if (!Sha256(key, &digest) || Base64UrlEncode(digest) != L"Yw3NKWbEM2aRElRIu7JbT_QSpJxzLbLIq8G4WBvXEN0") {
        std::cerr << "SHA-256 key ID mismatch\n";
        return 1;
    }
    std::cout << "BCrypt protocol vector tests passed\n";
    return 0;
}
