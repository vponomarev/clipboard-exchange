#include "crypto.h"

#include <windows.h>
#include <bcrypt.h>
#include <wincrypt.h>

#include <limits>

namespace {
struct Algorithm {
    BCRYPT_ALG_HANDLE value = nullptr;
    ~Algorithm() { if (value) BCryptCloseAlgorithmProvider(value, 0); }
};

struct Key {
    BCRYPT_KEY_HANDLE value = nullptr;
    ~Key() { if (value) BCryptDestroyKey(value); }
};

struct Hash {
    BCRYPT_HASH_HANDLE value = nullptr;
    ~Hash() { if (value) BCryptDestroyHash(value); }
};

bool AesKey(const std::vector<unsigned char>& raw, Algorithm* algorithm, Key* key,
            std::vector<unsigned char>* object) {
    if (raw.size() != 32 || BCryptOpenAlgorithmProvider(&algorithm->value, BCRYPT_AES_ALGORITHM,
                                                        nullptr, 0) < 0) return false;
    if (BCryptSetProperty(algorithm->value, BCRYPT_CHAINING_MODE,
                          reinterpret_cast<PUCHAR>(const_cast<wchar_t*>(BCRYPT_CHAIN_MODE_GCM)),
                          sizeof(BCRYPT_CHAIN_MODE_GCM), 0) < 0) return false;
    DWORD objectBytes = 0, copied = 0;
    if (BCryptGetProperty(algorithm->value, BCRYPT_OBJECT_LENGTH,
                          reinterpret_cast<PUCHAR>(&objectBytes), sizeof(objectBytes), &copied, 0) < 0) return false;
    object->resize(objectBytes);
    return BCryptGenerateSymmetricKey(algorithm->value, &key->value, object->data(), objectBytes,
                                      const_cast<PUCHAR>(raw.data()), static_cast<ULONG>(raw.size()), 0) >= 0;
}
}

bool Base64UrlDecode(const std::wstring& value, std::vector<unsigned char>* bytes) {
    if (!bytes) return false;
    std::wstring normalized = value;
    for (wchar_t& character : normalized) {
        if (character == L'-') character = L'+';
        else if (character == L'_') character = L'/';
    }
    while (normalized.size() % 4) normalized += L'=';
    DWORD size = 0;
    if (!CryptStringToBinaryW(normalized.c_str(), 0, CRYPT_STRING_BASE64, nullptr, &size, nullptr, nullptr)) return false;
    bytes->resize(size);
    return CryptStringToBinaryW(normalized.c_str(), 0, CRYPT_STRING_BASE64, bytes->data(), &size,
                                nullptr, nullptr) != FALSE;
}

std::wstring Base64UrlEncode(const std::vector<unsigned char>& bytes) {
    if (bytes.empty()) return {};
    DWORD characters = 0;
    const DWORD flags = CRYPT_STRING_BASE64 | CRYPT_STRING_NOCRLF;
    CryptBinaryToStringW(bytes.data(), static_cast<DWORD>(bytes.size()), flags, nullptr, &characters);
    std::vector<wchar_t> buffer(characters);
    if (!CryptBinaryToStringW(bytes.data(), static_cast<DWORD>(bytes.size()), flags,
                              buffer.data(), &characters)) return {};
    std::wstring value(buffer.data());
    while (!value.empty() && value.back() == L'=') value.pop_back();
    for (wchar_t& character : value) {
        if (character == L'+') character = L'-';
        else if (character == L'/') character = L'_';
    }
    return value;
}

bool Sha256(const std::vector<unsigned char>& input, std::vector<unsigned char>* digest) {
    if (!digest) return false;
    Algorithm algorithm;
    if (BCryptOpenAlgorithmProvider(&algorithm.value, BCRYPT_SHA256_ALGORITHM, nullptr, 0) < 0) return false;
    DWORD length = 0, copied = 0;
    if (BCryptGetProperty(algorithm.value, BCRYPT_HASH_LENGTH, reinterpret_cast<PUCHAR>(&length),
                          sizeof(length), &copied, 0) < 0) return false;
    DWORD objectLength = 0;
    if (BCryptGetProperty(algorithm.value, BCRYPT_OBJECT_LENGTH, reinterpret_cast<PUCHAR>(&objectLength),
                          sizeof(objectLength), &copied, 0) < 0) return false;
    std::vector<unsigned char> object(objectLength);
    Hash hash;
    if (BCryptCreateHash(algorithm.value, &hash.value, object.data(), objectLength,
                         nullptr, 0, 0) < 0) return false;
    digest->resize(length);
    return BCryptHashData(hash.value, const_cast<PUCHAR>(input.data()),
                          static_cast<ULONG>(input.size()), 0) >= 0 &&
           BCryptFinishHash(hash.value, digest->data(), length, 0) >= 0;
}

bool RandomBytes(size_t count, std::vector<unsigned char>* bytes) {
    if (!bytes || count > std::numeric_limits<ULONG>::max()) return false;
    bytes->resize(count);
    return BCryptGenRandom(nullptr, bytes->data(), static_cast<ULONG>(count),
                           BCRYPT_USE_SYSTEM_PREFERRED_RNG) >= 0;
}

bool Aes256GcmEncrypt(const std::vector<unsigned char>& raw,
                      const std::vector<unsigned char>& iv,
                      const std::vector<unsigned char>& aad,
                      const std::vector<unsigned char>& plaintext,
                      std::vector<unsigned char>* ciphertextAndTag) {
    if (!ciphertextAndTag || iv.size() != 12) return false;
    Algorithm algorithm;
    Key key;
    std::vector<unsigned char> object;
    if (!AesKey(raw, &algorithm, &key, &object)) return false;
    std::vector<unsigned char> ciphertext(plaintext.size());
    std::vector<unsigned char> tag(16);
    BCRYPT_AUTHENTICATED_CIPHER_MODE_INFO info;
    BCRYPT_INIT_AUTH_MODE_INFO(info);
    info.pbNonce = const_cast<PUCHAR>(iv.data());
    info.cbNonce = static_cast<ULONG>(iv.size());
    info.pbAuthData = const_cast<PUCHAR>(aad.data());
    info.cbAuthData = static_cast<ULONG>(aad.size());
    info.pbTag = tag.data();
    info.cbTag = static_cast<ULONG>(tag.size());
    ULONG written = 0;
    if (BCryptEncrypt(key.value, const_cast<PUCHAR>(plaintext.data()), static_cast<ULONG>(plaintext.size()),
                      &info, nullptr, 0, ciphertext.data(), static_cast<ULONG>(ciphertext.size()),
                      &written, 0) < 0 || written != ciphertext.size()) return false;
    ciphertextAndTag->assign(ciphertext.begin(), ciphertext.end());
    ciphertextAndTag->insert(ciphertextAndTag->end(), tag.begin(), tag.end());
    return true;
}

bool Aes256GcmDecrypt(const std::vector<unsigned char>& raw,
                      const std::vector<unsigned char>& iv,
                      const std::vector<unsigned char>& aad,
                      const std::vector<unsigned char>& ciphertextAndTag,
                      std::vector<unsigned char>* plaintext) {
    if (!plaintext || iv.size() != 12 || ciphertextAndTag.size() < 16) return false;
    Algorithm algorithm;
    Key key;
    std::vector<unsigned char> object;
    if (!AesKey(raw, &algorithm, &key, &object)) return false;
    const size_t cipherBytes = ciphertextAndTag.size() - 16;
    plaintext->resize(cipherBytes);
    BCRYPT_AUTHENTICATED_CIPHER_MODE_INFO info;
    BCRYPT_INIT_AUTH_MODE_INFO(info);
    info.pbNonce = const_cast<PUCHAR>(iv.data());
    info.cbNonce = static_cast<ULONG>(iv.size());
    info.pbAuthData = const_cast<PUCHAR>(aad.data());
    info.cbAuthData = static_cast<ULONG>(aad.size());
    info.pbTag = const_cast<PUCHAR>(ciphertextAndTag.data() + cipherBytes);
    info.cbTag = 16;
    ULONG written = 0;
    return BCryptDecrypt(key.value, const_cast<PUCHAR>(ciphertextAndTag.data()),
                         static_cast<ULONG>(cipherBytes), &info, nullptr, 0, plaintext->data(),
                         static_cast<ULONG>(plaintext->size()), &written, 0) >= 0 &&
           written == plaintext->size();
}
