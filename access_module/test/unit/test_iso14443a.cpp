/* Unit tier: ISO 14443A card selection against a model card.
 * No ESP-IDF, no FreeRTOS, no sdkconfig. Bare host compiler.
 * The model answers REQA, anticollision, select and HLTA the way a card in
 * the field does; each test injects at most one fault. */
#include "unity.h"
#include "iso14443a.hpp"
#include "mfrc522_status.hpp"
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

/* DRV-15 (F8): Transceive decoding. A timer IRQ is no card; CollErr on its
 * own is a collision (it used to fall outside the error mask and pass as a
 * good read); the other receive errors are read errors. */
void test_transceive_status_decoding(void) {
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_NO_CREDENTIAL,
                      mfrc522_transceive_status(MFRC522_IRQ_TIMER, 0));
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_NO_CREDENTIAL,
                      mfrc522_transceive_status(MFRC522_IRQ_TIMER | MFRC522_IRQ_IDLE, 0));
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_CREDENTIAL_COLLISION,
                      mfrc522_transceive_status(MFRC522_IRQ_RX | MFRC522_IRQ_ERR, MFRC522_ERR_COLL));
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_CREDENTIAL_COLLISION,
                      mfrc522_transceive_status(MFRC522_IRQ_ERR,
                                                MFRC522_ERR_COLL | MFRC522_ERR_PARITY));
    for (const uint8_t e : {MFRC522_ERR_PROTOCOL, MFRC522_ERR_PARITY, MFRC522_ERR_BUF_OVFL}) {
        TEST_ASSERT_EQUAL(PORTUNUS_ERR_CREDENTIAL_READ,
                          mfrc522_transceive_status(MFRC522_IRQ_ERR, e));
    }
    TEST_ASSERT_EQUAL(PORTUNUS_OK, mfrc522_transceive_status(MFRC522_IRQ_RX | MFRC522_IRQ_IDLE, 0));
}

/* DRV-15: a collision during anticollision ends the read with no UID. */
void test_collision_propagates(void) {
    for (int level = 0; level < 2; ++level) {
        Card c = card(UID7);
        c.err_level = level;
        c.err       = PORTUNUS_ERR_CREDENTIAL_COLLISION;
        credential_t cred;
        TEST_ASSERT_EQUAL(PORTUNUS_ERR_CREDENTIAL_COLLISION, read(c, cred));
        assert_zeroed(cred);
    }
}

/* DRV-13 (D-T7 a): a triple-size 10-byte UID needs CL3, which is not
 * implemented. The read fails closed instead of returning the first seven
 * bytes (cascade tag included) as a 7-byte UID, and CL3 is never sent. */
void test_10_byte_uid_fails_closed(void) {
    Card c = card(UID10);
    credential_t cred;
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_CREDENTIAL_READ, read(c, cred));
    assert_zeroed(cred);
    TEST_ASSERT_TRUE(c.sent_command(PICC_SEL_CL2));
    TEST_ASSERT_FALSE(c.sent_command(PICC_SEL_CL3));
}

/* SAK decides whether another cascade level follows. When the cascade tag
 * and SAK disagree at CL1 the frame is not trusted (AN10927: uid0 of a
 * single-size UID is never 88h). */
void test_cascade_tag_and_sak_must_agree(void) {
    credential_t cred;

    Card tag_but_complete = card(UID7);   /* CT at CL1, SAK says done */
    tag_but_complete.sak_level = 0;
    tag_but_complete.sak       = 0x08;
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_CREDENTIAL_READ, read(tag_but_complete, cred));
    assert_zeroed(cred);
    TEST_ASSERT_FALSE(tag_but_complete.sent_command(PICC_SEL_CL2));

    Card more_without_tag = card(UID4);   /* no CT, SAK says more */
    more_without_tag.sak_level = 0;
    more_without_tag.sak       = PICC_SAK_CASCADE;
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_CREDENTIAL_READ, read(more_without_tag, cred));
    assert_zeroed(cred);
    TEST_ASSERT_FALSE(more_without_tag.sent_command(PICC_SEL_CL2));

    /* 88h is reserved only as uid0 of a single-size UID: a double-size UID
     * whose uid3 is 88h reads normally, because at CL2 SAK alone decides. */
    const bytes uid7_88 = {0x04, 0x11, 0x22, 0x88, 0x44, 0x55, 0x66};
    Card tag_value_at_cl2 = card(uid7_88, 0x00);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, read(tag_value_at_cl2, cred));
    TEST_ASSERT_EQUAL_UINT8(7, cred.uid_len);
    TEST_ASSERT_EQUAL_HEX8_ARRAY(uid7_88.data(), cred.uid, 7);
}

/* A SAK reply that is not SAK plus CRC_A is a read error at every level. */
void test_short_sak_is_read_error(void) {
    for (int level = 0; level < 2; ++level) {
        Card c = card(UID7);
        c.short_sak_level = level;
        credential_t cred;
        TEST_ASSERT_EQUAL(PORTUNUS_ERR_CREDENTIAL_READ, read(c, cred));
        assert_zeroed(cred);
    }
}

/* A floating or shorted bus reads VersionReg as 0xFF or 0x00; the idle
 * read path and init both treat that as no chip. Real parts and the common
 * clones report other values. */
void test_version_dead_predicate(void) {
    TEST_ASSERT_TRUE(mfrc522_version_is_dead(0x00));
    TEST_ASSERT_TRUE(mfrc522_version_is_dead(0xFF));
    for (const uint8_t v : {0x91, 0x92, 0x88, 0x12, 0xB2}) {
        TEST_ASSERT_FALSE(mfrc522_version_is_dead(v));
    }
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
    RUN_TEST(test_transceive_status_decoding);
    RUN_TEST(test_collision_propagates);
    RUN_TEST(test_10_byte_uid_fails_closed);
    RUN_TEST(test_cascade_tag_and_sak_must_agree);
    RUN_TEST(test_short_sak_is_read_error);
    RUN_TEST(test_version_dead_predicate);
    RUN_TEST(test_null_credential);
    return UNITY_END();
}
