#!/usr/bin/env python3
from pathlib import Path


def replace_once(path: str, old: str, new: str) -> None:
    p = Path(path)
    text = p.read_text(encoding="utf-8")
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{path}: expected replacement exactly once, found {count}")
    p.write_text(text.replace(old, new, 1), encoding="utf-8")


esp = "components/esp32_network/src/websocket_voice_backend.cpp"
host = "host/companion_software_device/websocket_backend.cpp"
tests = "host/tests/tests.cpp"

# Capture the validated turn's freshness token while holding the same lock used
# to validate its identity. Enqueue turn-scoped events with that captured token
# instead of re-reading the current generation after the lock is released.
replace_once(
    esp,
    '''  if (turn_scoped ||
      (type == protocol::ControlType::protocol_error && !incoming_turn_id.empty())) {
    if (!active_turn_matches(incoming_turn_id)) {
      cJSON_Delete(root);
      return;
    }
  }

  if (type == protocol::ControlType::voice_mail_available) {''',
    '''  uint64_t turn_session_epoch = 0;
  uint64_t turn_media_generation = 0;
  if (turn_scoped ||
      (type == protocol::ControlType::protocol_error && !incoming_turn_id.empty())) {
    taskENTER_CRITICAL(&turn_id_lock_);
    const bool current_turn = (turn_active_.load() || tts_active_.load()) &&
                              incoming_turn_id == active_turn_id_.data();
    if (current_turn) {
      turn_session_epoch = session_epoch_.load();
      turn_media_generation = media_generation_.load();
    }
    taskEXIT_CRITICAL(&turn_id_lock_);
    if (!current_turn) {
      cJSON_Delete(root);
      return;
    }
  }
  const auto enqueue_turn_event = [&](BackendEvent& event) {
    event.scope = BackendEventScope::generation;
    event.session_epoch = turn_session_epoch;
    event.generation = turn_media_generation;
    return xQueueSend(event_queue_, &event, 0) == pdPASS;
  };

  if (type == protocol::ControlType::voice_mail_available) {''')

replace_once(
    esp,
    '''  } else if (type == protocol::ControlType::transcript_final) {
    enqueue_event(BackendEventType::transcript, json_string(payload, "text"));
  } else if (type == protocol::ControlType::tts_lifecycle) {
    const std::string_view state = json_string(payload, "state");
    if (state == "start") {
      if (activate_tts_for_matching_turn(incoming_turn_id))
        enqueue_event(BackendEventType::tts_started);
    } else if (state == "sentence_start") {
      enqueue_event(BackendEventType::tts_sentence, json_string(payload, "text"));
    } else if (state == "stop") {
      if (deactivate_matching_turn(incoming_turn_id))
        enqueue_event(BackendEventType::tts_finished);
    }
  } else if (type == protocol::ControlType::alarm_fired) {''',
    '''  } else if (type == protocol::ControlType::transcript_final) {
    BackendEvent event{};
    event.type = BackendEventType::transcript;
    event.set_text(json_string(payload, "text"));
    (void)enqueue_turn_event(event);
  } else if (type == protocol::ControlType::tts_lifecycle) {
    const std::string_view state = json_string(payload, "state");
    if (state == "start") {
      if (activate_tts_for_matching_turn(incoming_turn_id)) {
        BackendEvent event{};
        event.type = BackendEventType::tts_started;
        (void)enqueue_turn_event(event);
      }
    } else if (state == "sentence_start") {
      BackendEvent event{};
      event.type = BackendEventType::tts_sentence;
      event.set_text(json_string(payload, "text"));
      (void)enqueue_turn_event(event);
    } else if (state == "stop") {
      if (deactivate_matching_turn(incoming_turn_id)) {
        BackendEvent event{};
        event.type = BackendEventType::tts_finished;
        (void)enqueue_turn_event(event);
      }
    }
  } else if (type == protocol::ControlType::alarm_fired) {''')

replace_once(
    esp,
    '''  } else if (type == protocol::ControlType::ui_card) {
    PresentationCardV1 card{};
    if (parse_presentation_card(payload, card)) enqueue_card_event(card);
  } else if (type == protocol::ControlType::ui_state) {
    PresentationHint hint{};
    if (parse_presentation_hint(payload, hint)) enqueue_hint_event(hint);
  } else if (type == protocol::ControlType::agent_status) {
    AgentPresentationStatus status{};
    if (parse_agent_presentation_status(payload, status))
      enqueue_agent_status_event(status);
  } else if (type == protocol::ControlType::turn_state) {''',
    '''  } else if (type == protocol::ControlType::ui_card) {
    PresentationCardV1 card{};
    if (parse_presentation_card(payload, card)) {
      BackendEvent event{};
      event.type = BackendEventType::presentation_card;
      event.set_card(card);
      (void)enqueue_turn_event(event);
    }
  } else if (type == protocol::ControlType::ui_state) {
    PresentationHint hint{};
    if (parse_presentation_hint(payload, hint)) {
      BackendEvent event{};
      event.type = BackendEventType::presentation_hint;
      event.set_hint(hint);
      (void)enqueue_turn_event(event);
    }
  } else if (type == protocol::ControlType::agent_status) {
    AgentPresentationStatus status{};
    if (parse_agent_presentation_status(payload, status)) {
      BackendEvent event{};
      event.type = BackendEventType::agent_status;
      event.set_agent_status(status);
      (void)enqueue_turn_event(event);
    }
  } else if (type == protocol::ControlType::turn_state) {''')

# clear_voice_mail() advances the voicemail generation. Advance first so the
# terminal failure event is stamped with the generation poll_event() expects.
replace_once(
    esp,
    '''    if (matches && !lease_valid) {
      enqueue_voice_mail_event(BackendEventType::voice_mail_failed, item,
                               "VOICE MAIL CLAIM EXPIRED");
      clear_voice_mail(true);
      cJSON_Delete(root);
      return;
    }
    if (matches && !enqueue_media_job(item, json_string(payload, "playback_id"),
                                      json_string(payload, "media_ref"))) {
      enqueue_voice_mail_event(BackendEventType::voice_mail_failed, item,
                               "VOICE MAIL DOWNLOAD BUSY");
      clear_voice_mail(true);
    }''',
    '''    if (matches && !lease_valid) {
      clear_voice_mail(true);
      enqueue_voice_mail_event(BackendEventType::voice_mail_failed, item,
                               "VOICE MAIL CLAIM EXPIRED");
      cJSON_Delete(root);
      return;
    }
    if (matches && !enqueue_media_job(item, json_string(payload, "playback_id"),
                                      json_string(payload, "media_ref"))) {
      clear_voice_mail(true);
      enqueue_voice_mail_event(BackendEventType::voice_mail_failed, item,
                               "VOICE MAIL DOWNLOAD BUSY");
    }''')

# Mirror the same captured-token semantics in the software-device oracle.
replace_once(
    host,
    '''    auto current_turn = [&] {
      std::lock_guard lock(state_mutex_);
      return active_turn_id_;
    }();
    const bool turn_scoped = type == protocol::ControlType::turn_state ||
                             type == protocol::ControlType::transcript_final ||
                             type == protocol::ControlType::tts_lifecycle ||
                             type == protocol::ControlType::agent_status ||
                             type == protocol::ControlType::ui_card ||
                             type == protocol::ControlType::ui_state;
    if (turn_scoped && incoming_turn != current_turn) {
      std::lock_guard lock(state_mutex_);
      ++stats_.stale_controls;
      return;
    }

    switch (type) {''',
    '''    const bool turn_scoped = type == protocol::ControlType::turn_state ||
                             type == protocol::ControlType::transcript_final ||
                             type == protocol::ControlType::tts_lifecycle ||
                             type == protocol::ControlType::agent_status ||
                             type == protocol::ControlType::ui_card ||
                             type == protocol::ControlType::ui_state;
    uint64_t turn_session_epoch = 0;
    uint64_t turn_media_generation = 0;
    if (turn_scoped) {
      std::lock_guard lock(state_mutex_);
      if ((!turn_active_ && !tts_active_) || incoming_turn != active_turn_id_) {
        ++stats_.stale_controls;
        return;
      }
      turn_session_epoch = connection_generation_.load();
      turn_media_generation = media_generation_.load();
    }
    const auto enqueue_turn_event = [&](BackendEvent event) {
      std::lock_guard lock(state_mutex_);
      if (events_.size() == kMaximumEvents) events_.pop_front();
      event.scope = BackendEventScope::generation;
      event.session_epoch = turn_session_epoch;
      event.generation = turn_media_generation;
      events_.push_back(event);
    };

    switch (type) {''')

replace_once(
    host,
    '''    case protocol::ControlType::transcript_final:
      enqueue_event(BackendEventType::transcript, json_string(payload, "text"));
      break;
    case protocol::ControlType::tts_lifecycle: {
      const std::string state = json_string(payload, "state");
      if (state == "start") {
        {
          std::lock_guard lock(state_mutex_);
          tts_active_ = true;
        }
        enqueue_event(BackendEventType::tts_started);
      } else if (state == "sentence_start") {
        enqueue_event(BackendEventType::tts_sentence, json_string(payload, "text"));
      } else if (state == "stop") {
        {
          std::lock_guard lock(state_mutex_);
          tts_active_ = false;
          turn_active_ = false;
        }
        enqueue_event(BackendEventType::tts_finished);
      }
      break;
    }''',
    '''    case protocol::ControlType::transcript_final: {
      BackendEvent event{};
      event.type = BackendEventType::transcript;
      event.set_text(json_string(payload, "text"));
      enqueue_turn_event(event);
      break;
    }
    case protocol::ControlType::tts_lifecycle: {
      const std::string state = json_string(payload, "state");
      if (state == "start") {
        bool current = false;
        {
          std::lock_guard lock(state_mutex_);
          current = turn_active_ && incoming_turn == active_turn_id_ &&
                    connection_generation_.load() == turn_session_epoch &&
                    media_generation_.load() == turn_media_generation;
          if (current) tts_active_ = true;
        }
        if (current) {
          BackendEvent event{};
          event.type = BackendEventType::tts_started;
          enqueue_turn_event(event);
        }
      } else if (state == "sentence_start") {
        BackendEvent event{};
        event.type = BackendEventType::tts_sentence;
        event.set_text(json_string(payload, "text"));
        enqueue_turn_event(event);
      } else if (state == "stop") {
        bool current = false;
        {
          std::lock_guard lock(state_mutex_);
          current = (turn_active_ || tts_active_) && incoming_turn == active_turn_id_ &&
                    connection_generation_.load() == turn_session_epoch &&
                    media_generation_.load() == turn_media_generation;
          if (current) {
            tts_active_ = false;
            turn_active_ = false;
          }
        }
        if (current) {
          BackendEvent event{};
          event.type = BackendEventType::tts_finished;
          enqueue_turn_event(event);
        }
      }
      break;
    }''')

replace_once(
    host,
    '''    case protocol::ControlType::ui_card: {
      PresentationCardV1 card{};
      if (!parse_presentation_card(payload, card)) {
        enqueue_event(BackendEventType::error, "INVALID UI CARD");
        break;
      }
      enqueue_card_event(card);
      break;
    }
    case protocol::ControlType::ui_state: {
      PresentationHint hint{};
      if (!parse_presentation_hint(payload, hint)) {
        enqueue_event(BackendEventType::error, "INVALID UI STATE");
        break;
      }
      enqueue_hint_event(hint);
      break;
    }
    case protocol::ControlType::agent_status: {
      AgentPresentationStatus status{};
      if (!parse_agent_presentation_status(payload, status)) {
        enqueue_event(BackendEventType::error, "INVALID AGENT STATUS");
        break;
      }
      enqueue_agent_status_event(status);
      break;
    }''',
    '''    case protocol::ControlType::ui_card: {
      PresentationCardV1 card{};
      if (!parse_presentation_card(payload, card)) {
        enqueue_event(BackendEventType::error, "INVALID UI CARD");
        break;
      }
      BackendEvent event{};
      event.type = BackendEventType::presentation_card;
      event.set_card(card);
      enqueue_turn_event(event);
      break;
    }
    case protocol::ControlType::ui_state: {
      PresentationHint hint{};
      if (!parse_presentation_hint(payload, hint)) {
        enqueue_event(BackendEventType::error, "INVALID UI STATE");
        break;
      }
      BackendEvent event{};
      event.type = BackendEventType::presentation_hint;
      event.set_hint(hint);
      enqueue_turn_event(event);
      break;
    }
    case protocol::ControlType::agent_status: {
      AgentPresentationStatus status{};
      if (!parse_agent_presentation_status(payload, status)) {
        enqueue_event(BackendEventType::error, "INVALID AGENT STATUS");
        break;
      }
      BackendEvent event{};
      event.type = BackendEventType::agent_status;
      event.set_agent_status(status);
      enqueue_turn_event(event);
      break;
    }''')

# Make stale-card assertions observe the card path instead of inheriting the
# Display base implementation, which always returned false without recording.
replace_once(
    tests,
    '''struct FakeDisplay final : Display {
  std::vector<std::pair<UiState, std::string>> events;
  void show(UiState state, std::string_view text) override {
    events.emplace_back(state, text);
  }
};''',
    '''struct FakeDisplay final : Display {
  std::vector<std::pair<UiState, std::string>> events;
  void show(UiState state, std::string_view text) override {
    events.emplace_back(state, text);
  }
  bool show_card(UiState state, const PresentationCardV1& card) override {
    const std::string_view text = card.primary_view().empty()
                                      ? card.title_view()
                                      : card.primary_view();
    events.emplace_back(state, text);
    return true;
  }
};''')
