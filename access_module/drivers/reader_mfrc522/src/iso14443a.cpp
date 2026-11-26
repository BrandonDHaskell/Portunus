#include "iso14443a.hpp"
#include "error_codes.hpp"

#include <string.h>

/** Anticollision and select for one cascade level. On success part holds the
 *  level's 4 UID bytes (or cascade tag plus 3 bytes) followed by the BCC. */
static portunus_err_t anticoll_select(const iso14443a_io_t &io, uint8_t sel, uint8_t part[5])
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

    uint8_t sak[3] = {};  /* SAK plus CRC_A */
    rx_len = sizeof sak;
    return io.transceive(io.ctx, select, sizeof select, 0, sak, &rx_len);
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

    credential_t out = {};
    uint8_t cl1[5];
    err = anticoll_select(io, PICC_SEL_CL1, cl1);
    if (err != PORTUNUS_OK) {
        return err;
    }

    if (cl1[0] == PICC_CASCADE_TAG) {
        memcpy(&out.uid[0], &cl1[1], 3);
        uint8_t cl2[5];
        err = anticoll_select(io, PICC_SEL_CL2, cl2);
        if (err != PORTUNUS_OK) {
            return err;
        }
        memcpy(&out.uid[3], cl2, 4);
        out.uid_len = 7;
    } else {
        memcpy(out.uid, cl1, 4);
        out.uid_len = 4;
    }

    *cred = out;
    return PORTUNUS_OK;
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
