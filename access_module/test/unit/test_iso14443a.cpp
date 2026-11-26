/* Unit tier: ISO 14443A card selection against a model card.
 * No ESP-IDF, no FreeRTOS, no sdkconfig. Bare host compiler.
 * The model answers REQA, anticollision, select and HLTA the way a card in
 * the field does; each test injects at most one fault. */
#include "unity.h"
#include "iso14443a.hpp"
#include "error_codes.hpp"

#include <string.h>
#include <algorithm>
#include <vector>

void setUp(void) {}
void tearDown(void) {}

using bytes = std::vector<uint8_t>;

/* CRC_A per ISO/IEC 14443-3 Annex B: preset 0x6363, reflected 0x8408. */
static void crc_a_sw(const uint8_t *data, size_t len, uint8_t out[2]) {
    uint16_t crc = 0x6363;
    for (size_t i = 0; i < len; ++i) {
        uint8_t b = data[i] ^ static_cast<uint8_t>(crc & 0xFF);
        b ^= static_cast<uint8_t>(b << 4);
        crc = static_cast<uint16_t>((crc >> 8) ^ (b << 8) ^ (b << 3) ^ (b >> 4));
    }
    out[0] = static_cast<uint8_t>(crc & 0xFF);
    out[1] = static_cast<uint8_t>(crc >> 8);
}

struct Card {
    bytes   uid;              /* 4, 7 or 10 bytes; empty means no card */
    uint8_t final_sak = 0x08; /* SAK once the UID is complete */

    /* Fault injection, by cascade level (0 = CL1); -1 disables. */
    int            bad_bcc_level   = -1;
    int            err_level       = -1;  /* anticollision returns err */
    portunus_err_t err             = PORTUNUS_OK;
    int            sak_level       = -1;  /* replace that level's SAK */
    uint8_t        sak             = 0;
    int            short_sak_level = -1;  /* reply with the SAK byte only */
    uint8_t        atqa_len        = 2;

    std::vector<bytes> sent;              /* every frame the reader sent */
    std::vector<uint8_t> sent_bits;       /* tx_last_bits for each frame */

    int levels() const { return uid.size() == 4 ? 1 : uid.size() == 7 ? 2 : 3; }

    /* The 4 bytes this card answers at a cascade level, before the BCC. */
    bytes part(int level) const {
        const bool last = level == levels() - 1;
        const size_t off = static_cast<size_t>(level) * 3;
        if (last) return bytes(uid.begin() + off, uid.begin() + off + 4);
        return {PICC_CASCADE_TAG, uid[off], uid[off + 1], uid[off + 2]};
    }

    portunus_err_t transceive(const uint8_t *tx, uint8_t tx_len, uint8_t bits,
                              uint8_t *rx, uint8_t *rx_len) {
        sent.emplace_back(tx, tx + tx_len);
        sent_bits.push_back(bits);
        if (uid.empty()) return PORTUNUS_ERR_NO_CREDENTIAL;

        auto reply = [&](const bytes &r) {
            const uint8_t n = static_cast<uint8_t>(r.size()) < *rx_len
                                  ? static_cast<uint8_t>(r.size()) : *rx_len;
            memcpy(rx, r.data(), n);
            *rx_len = n;
            return PORTUNUS_OK;
        };

        if (tx_len == 1 && tx[0] == PICC_REQA && bits == 7) {
            bytes atqa = {0x44, 0x00};
            atqa.resize(atqa_len);
            return reply(atqa);
        }
        if (tx[0] == PICC_HLTA) return PORTUNUS_ERR_NO_CREDENTIAL;  /* no answer */

        const int level = tx[0] == PICC_SEL_CL1 ? 0 : tx[0] == PICC_SEL_CL2 ? 1
                        : tx[0] == PICC_SEL_CL3 ? 2 : -1;
        if (level < 0 || level >= levels()) return PORTUNUS_ERR_NO_CREDENTIAL;

        bytes p = part(level);
        p.push_back(p[0] ^ p[1] ^ p[2] ^ p[3]);

        if (tx_len == 2 && tx[1] == PICC_NVB_ANTICOLL) {
            if (level == err_level) return err;
            if (level == bad_bcc_level) p[4] ^= 0x01;
            return reply(p);
        }
        if (tx_len == 9 && tx[1] == PICC_NVB_SELECT) {
            /* A card ignores a select that is not for it or fails its CRC. */
            uint8_t crc[2];
            crc_a_sw(tx, 7, crc);
            if (!std::equal(p.begin(), p.end(), tx + 2) || crc[0] != tx[7] || crc[1] != tx[8]) {
                return PORTUNUS_ERR_NO_CREDENTIAL;
            }
            uint8_t s = level == levels() - 1 ? final_sak : PICC_SAK_CASCADE;
            if (level == sak_level) s = sak;
            bytes r = {s, 0, 0};
            crc_a_sw(r.data(), 1, &r[1]);
            if (level == short_sak_level) r.resize(1);
            return reply(r);
        }
        return PORTUNUS_ERR_NO_CREDENTIAL;
    }

    bool sent_command(uint8_t cmd) const {
        for (const bytes &f : sent) if (!f.empty() && f[0] == cmd) return true;
        return false;
    }
};

static Card card(const bytes &uid, uint8_t final_sak = 0x08) {
    Card c;
    c.uid       = uid;
    c.final_sak = final_sak;
    return c;
}

static portunus_err_t io_transceive(void *ctx, const uint8_t *tx, uint8_t tx_len, uint8_t bits,
                                    uint8_t *rx, uint8_t *rx_len) {
    return static_cast<Card *>(ctx)->transceive(tx, tx_len, bits, rx, rx_len);
}

static portunus_err_t io_crc_a(void *, const uint8_t *data, uint8_t len, uint8_t out[2]) {
    crc_a_sw(data, len, out);
    return PORTUNUS_OK;
}

static iso14443a_io_t io_for(Card &c) { return {&c, io_transceive, io_crc_a}; }

static portunus_err_t read(Card &c, credential_t &cred) {
    memset(&cred, 0xA5, sizeof cred);  /* prove failures leave it zeroed */
    return iso14443a_read_uid(io_for(c), &cred);
}

static void assert_zeroed(const credential_t &cred) {
    const credential_t zero = {};
    TEST_ASSERT_EQUAL_MEMORY(&zero, &cred, sizeof cred);
}

static const bytes UID4  = {0xDE, 0xAD, 0xBE, 0xEF};
static const bytes UID7  = {0x04, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66};
static const bytes UID10 = {0x04, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99};

/* DRV-11: single-size 4-byte UID. REQA is a 7-bit short frame, then CL1
 * anticollision and a CRC-protected select. */
void test_reads_4_byte_uid(void) {
    Card c = card(UID4);
    credential_t cred;
    TEST_ASSERT_EQUAL(PORTUNUS_OK, read(c, cred));
    TEST_ASSERT_EQUAL_UINT8(4, cred.uid_len);
    TEST_ASSERT_EQUAL_HEX8_ARRAY(UID4.data(), cred.uid, 4);

    TEST_ASSERT_EQUAL_size_t(3, c.sent.size());
    TEST_ASSERT_TRUE((c.sent[0] == bytes{PICC_REQA}));
    TEST_ASSERT_EQUAL_UINT8(7, c.sent_bits[0]);
    TEST_ASSERT_TRUE((c.sent[1] == bytes{PICC_SEL_CL1, PICC_NVB_ANTICOLL}));
    const bytes sel = {PICC_SEL_CL1, PICC_NVB_SELECT, 0xDE, 0xAD, 0xBE, 0xEF, 0x22};
    TEST_ASSERT_EQUAL_HEX8_ARRAY(sel.data(), c.sent[2].data(), 7);
}

/* DRV-12: double-size 7-byte UID. CL1 carries the cascade tag and the first
 * three bytes, CL2 the last four. */
void test_reads_7_byte_uid(void) {
    Card c = card(UID7, 0x00);
    credential_t cred;
    TEST_ASSERT_EQUAL(PORTUNUS_OK, read(c, cred));
    TEST_ASSERT_EQUAL_UINT8(7, cred.uid_len);
    TEST_ASSERT_EQUAL_HEX8_ARRAY(UID7.data(), cred.uid, 7);
    TEST_ASSERT_TRUE(c.sent_command(PICC_SEL_CL2));
    TEST_ASSERT_FALSE(c.sent_command(PICC_SEL_CL3));
}

/* DRV-14: a BCC mismatch at either level is a read error, the card is not
 * selected at that level, and no UID comes back. */
void test_bcc_mismatch_is_read_error(void) {
    for (int level = 0; level < 2; ++level) {
        Card c = card(UID7);
        c.bad_bcc_level = level;
        credential_t cred;
        TEST_ASSERT_EQUAL(PORTUNUS_ERR_CREDENTIAL_READ, read(c, cred));
        assert_zeroed(cred);
        TEST_ASSERT_EQUAL_UINT8(level == 0 ? PICC_SEL_CL1 : PICC_SEL_CL2, c.sent.back()[0]);
        TEST_ASSERT_EQUAL_UINT8(PICC_NVB_ANTICOLL, c.sent.back()[1]);
    }
}

/* DRV-16: HLTA is 50 00 followed by its CRC_A (57 CD), and the card's
 * silence is not an error. */
void test_halt_sends_hlta_with_crc(void) {
    Card c = card(UID4);
    iso14443a_halt(io_for(c));
    TEST_ASSERT_EQUAL_size_t(1, c.sent.size());
    const bytes hlta = {0x50, 0x00, 0x57, 0xCD};
    TEST_ASSERT_TRUE(c.sent[0] == hlta);
}

/* No card in the field: REQA times out and that is reported as is. */
void test_no_card(void) {
    Card c;
    credential_t cred;
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_NO_CREDENTIAL, read(c, cred));
    assert_zeroed(cred);
    TEST_ASSERT_EQUAL_size_t(1, c.sent.size());
}

/* A truncated ATQA, or a chip error mid-cascade, fails without a UID. */
void test_short_atqa_and_chip_error(void) {
    Card a = card(UID4);
    a.atqa_len = 1;
    credential_t cred;
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_CREDENTIAL_READ, read(a, cred));
    assert_zeroed(cred);

    Card b = card(UID7);
    b.err_level = 1;
    b.err       = PORTUNUS_ERR_SPI_TRANSFER;
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_SPI_TRANSFER, read(b, cred));
    assert_zeroed(cred);
}

void test_null_credential(void) {
    Card c = card(UID4);
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_INVALID_ARG, iso14443a_read_uid(io_for(c), nullptr));
    TEST_ASSERT_TRUE(c.sent.empty());
}

int main(void) {
    UNITY_BEGIN();
    RUN_TEST(test_reads_4_byte_uid);
    RUN_TEST(test_reads_7_byte_uid);
    RUN_TEST(test_bcc_mismatch_is_read_error);
    RUN_TEST(test_halt_sends_hlta_with_crc);
    RUN_TEST(test_no_card);
    RUN_TEST(test_short_atqa_and_chip_error);
    RUN_TEST(test_null_credential);
    return UNITY_END();
}
