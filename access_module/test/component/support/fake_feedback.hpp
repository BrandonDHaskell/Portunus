#pragma once
#include "i_feedback.hpp"
#include <vector>

class FakeFeedback : public IFeedback {
public:
    std::vector<feedback_type_t> indications;

    portunus_err_t init() override { return PORTUNUS_OK; }

    void indicate(feedback_type_t type) override {
        indications.push_back(type);
    }

    void reset() { indications.clear(); }

    /* Returns the last indicated type, or NONE if none yet. */
    feedback_type_t last() const {
        return indications.empty() ? feedback_type_t::NONE : indications.back();
    }

    int count_of(feedback_type_t type) const {
        int n = 0;
        for (auto t : indications) if (t == type) n++;
        return n;
    }
};
