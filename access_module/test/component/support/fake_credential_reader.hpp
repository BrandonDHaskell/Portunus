#pragma once
#include "i_credential_reader.hpp"
#include "error_codes.hpp"
#include <queue>

class FakeCredentialReader : public ICredentialReader {
public:
    /* Push credentials to be returned by successive read() calls. */
    void enqueue(const credential_t &cred) { m_queue.push({PORTUNUS_OK, cred}); }
    void enqueue_error(portunus_err_t err) { credential_t c{}; m_queue.push({err, c}); }

    int halts = 0;

    portunus_err_t init() override { return PORTUNUS_OK; }

    portunus_err_t read(credential_t *out) override {
        if (m_queue.empty()) return PORTUNUS_ERR_NO_CREDENTIAL;
        auto [err, cred] = m_queue.front();
        m_queue.pop();
        if (err == PORTUNUS_OK && out) *out = cred;
        return err;
    }

    void halt() override { halts++; }

private:
    struct Entry { portunus_err_t err; credential_t cred; };
    std::queue<Entry> m_queue;
};
