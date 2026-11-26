#include "iso14443a.hpp"
#include "error_codes.hpp"

#include <string.h>

/** Cascade levels this driver selects. A triple-size UID needs CL3 and is
 *  rejected (D-T7 option a): no deployed card uses one. */
static constexpr int MAX_CASCADE_LEVELS = 2;

/** Anticollision and select for one cascade level. On success part holds the
 *  level's 4 UID bytes (or cascade tag plus 3 bytes) followed by the BCC,
 *  and *sak the card's SAK. */
static portunus_err_t anticoll_select(const iso14443a_io_t &io, uint8_t sel, uint8_t part[5],
                                      uint8_t *sak)
{
    const uint8_t anticoll[2] = { sel, PICC_NVB_ANTICOLL };
    uint8_t rx_len = 5;
    portunus_err_t err = io.transceive(io.ctx, anticoll, sizeof anticoll, 0, part, &rx_len);
    if (err != PORTUNUS_OK) {
        return err;
    }
    if (rx_len != 5) {
        return PORTUNUS_ERR_CREDENTIAL_READ;
    }
    if ((part[0] ^ part[1] ^ part[2] ^ part[3]) != part[4]) {
        return PORTUNUS_ERR_CREDENTIAL_READ;  /* BCC mismatch */
    }

    /* SEL, NVB, 4 UID bytes, BCC, CRC_A. */
    uint8_t select[9] = { sel, PICC_NVB_SELECT };
    memcpy(&select[2], part, 5);
    err = io.crc_a(io.ctx, select, 7, &select[7]);
    if (err != PORTUNUS_OK) {
        return err;
    }

    uint8_t reply[3] = {};  /* SAK plus CRC_A */
    rx_len = sizeof reply;
    err = io.transceive(io.ctx, select, sizeof select, 0, reply, &rx_len);
    if (err != PORTUNUS_OK) {
        return err;
    }
    if (rx_len != sizeof reply) {
        return PORTUNUS_ERR_CREDENTIAL_READ;
    }
    *sak = reply[0];
    return PORTUNUS_OK;
}

portunus_err_t iso14443a_read_uid(const iso14443a_io_t &io, credential_t *cred)
{
    if (cred == NULL) {
        return PORTUNUS_ERR_INVALID_ARG;
    }
    memset(cred, 0, sizeof *cred);

    /* REQA is a 7-bit short frame; the card answers with a 2-byte ATQA. */
    const uint8_t reqa = PICC_REQA;
    uint8_t atqa[2];
    uint8_t rx_len = sizeof atqa;
    portunus_err_t err = io.transceive(io.ctx, &reqa, 1, 7, atqa, &rx_len);
    if (err != PORTUNUS_OK) {
        return err;
    }
    if (rx_len != sizeof atqa) {
        return PORTUNUS_ERR_CREDENTIAL_READ;
    }

    /* SAK says whether another cascade level follows; the cascade tag says
     * where this level's UID bytes start. They must agree, and ISO/IEC
     * 14443-3 reserves 88h so it is never uid0 of a single-size UID. At CL2
     * SAK alone decides, so a double-size uid3 of 88h is accepted. */
    static const uint8_t SEL[MAX_CASCADE_LEVELS] = { PICC_SEL_CL1, PICC_SEL_CL2 };
    credential_t out = {};
    for (int level = 0; level < MAX_CASCADE_LEVELS; ++level) {
        uint8_t part[5];
        uint8_t sak = 0;
        err = anticoll_select(io, SEL[level], part, &sak);
        if (err != PORTUNUS_OK) {
            return err;
        }
        const bool more = (sak & PICC_SAK_CASCADE) != 0;
        const bool tag  = part[0] == PICC_CASCADE_TAG;
        if (more != tag && (more || level == 0)) {
            return PORTUNUS_ERR_CREDENTIAL_READ;
        }
        if (!more) {
            memcpy(&out.uid[out.uid_len], part, 4);
            out.uid_len += 4;
            *cred = out;
            return PORTUNUS_OK;
        }
        memcpy(&out.uid[out.uid_len], &part[1], 3);
        out.uid_len += 3;
    }

    /* Still incomplete after CL2: a 10-byte UID. Fail closed rather than
     * hand back a truncated UID that could match or enroll the wrong card. */
    return PORTUNUS_ERR_CREDENTIAL_READ;
}

void iso14443a_halt(const iso14443a_io_t &io)
{
    uint8_t hlta[4] = { PICC_HLTA, 0x00 };
    if (io.crc_a(io.ctx, hlta, 2, &hlta[2]) != PORTUNUS_OK) {
        return;
    }
    uint8_t rx_len = 0;
    (void)io.transceive(io.ctx, hlta, sizeof hlta, 0, NULL, &rx_len);
}
