extends SceneTree

const PROTOCOL := 2
const MAX_REQUEST := 65536
var peer := StreamPeerTCP.new()
var started := false
var width := 144
var height := 40
var pixel_aspect := 0.5
var logical_size: Vector2i
var native_resolution := false
var input_bytes := PackedByteArray()
var pressed_codes: Array[int] = []

func _initialize() -> void:
	var args := OS.get_cmdline_user_args()
	if args.size() != 2:
		printerr("Termcade runtime requires its host connection.")
		quit(2)
		return
	var err := peer.connect_to_host("127.0.0.1", int(args[0]))
	if err != OK:
		quit(2)
		return
	while peer.get_status() == StreamPeerTCP.STATUS_CONNECTING:
		peer.poll()
		OS.delay_usec(1000)
	peer.set_no_delay(true)
	logical_size = Vector2i(ProjectSettings.get_setting("display/window/size/viewport_width", 0), ProjectSettings.get_setting("display/window/size/viewport_height", 0))
	if logical_size.x <= 0 or logical_size.y <= 0:
		logical_size = root.size
	native_resolution = OS.get_environment("TERMCADE_GODOT_FULL_RES") == "1"
	# Keep authored coordinates and layout while rendering only the pixels
	# the terminal can show. Native resolution remains available for games
	# whose scripts or shaders depend on the original render target size.
	if not native_resolution:
		root.content_scale_size = logical_size
		root.content_scale_mode = Window.CONTENT_SCALE_MODE_CANVAS_ITEMS
		root.content_scale_aspect = Window.CONTENT_SCALE_ASPECT_KEEP
	root.disable_3d = false
	RenderingServer.render_loop_enabled = false
	_send({"protocol": PROTOCOL, "token": args[1], "title": ProjectSettings.get_setting("application/config/name", "Godot game")})

func _process(_delta: float) -> bool:
	# Host-driven fixed-FPS engine iterations. While the arcade is paused or
	# waiting on input, Godot cannot run ahead and accumulate invisible frames.
	while peer.get_status() == StreamPeerTCP.STATUS_CONNECTED:
		peer.poll()
		var available := peer.get_available_bytes()
		if available > 0:
			var result := peer.get_data(mini(available, MAX_REQUEST))
			if result[0] != OK:
				return true
			input_bytes.append_array(result[1])
			if input_bytes.size() > MAX_REQUEST:
				return true
			var newline := input_bytes.find(10)
			if newline >= 0:
				var request = JSON.parse_string(input_bytes.slice(0, newline).get_string_from_utf8())
				input_bytes = input_bytes.slice(newline + 1)
				if not request is Dictionary:
					return true
				return _request(request)
		OS.delay_usec(1000)
	return true

func _request(request: Dictionary) -> bool:
	if request.get("op") == "close":
		return true
	width = int(request.get("width", width))
	height = int(request.get("height", height))
	pixel_aspect = float(request.get("pixel_aspect", pixel_aspect))
	if width < 1 or width > 600 or height < 1 or height > 360 or pixel_aspect <= 0 or pixel_aspect > 2:
		_send({"error": "invalid framebuffer dimensions"})
		return true
	if not native_resolution:
		var scale := minf(1.0, minf(float(width) / logical_size.x, float(height) / pixel_aspect / logical_size.y))
		var render_size := Vector2i(maxi(1, roundi(logical_size.x * scale)), maxi(1, roundi(logical_size.y * scale)))
		if root.size != render_size:
			root.size = render_size
	if request.get("op") == "reset":
		for code in pressed_codes:
			var release := InputEventKey.new()
			release.keycode = code
			release.physical_keycode = code
			Input.parse_input_event(release)
		pressed_codes.clear()
		Input.flush_buffered_events()
		if is_instance_valid(current_scene):
			current_scene.free()
		var packed := load(ProjectSettings.get_setting("application/run/main_scene", "")) as PackedScene
		if packed == null:
			_send({"error": "cannot load main scene"})
			return true
		paused = false
		var scene := packed.instantiate()
		root.add_child(scene)
		current_scene = scene
		started = true
	elif request.get("op") != "step" or not started:
		_send({"error": "expected reset or step"})
		return true
	for key in request.get("keys", []):
		var event := InputEventKey.new()
		event.keycode = int(key.code)
		event.physical_keycode = event.keycode
		event.pressed = bool(key.down)
		event.echo = event.pressed and pressed_codes.has(event.keycode)
		if event.pressed and not pressed_codes.has(event.keycode):
			pressed_codes.append(event.keycode)
		elif not event.pressed:
			pressed_codes.erase(event.keycode)
		Input.parse_input_event(event)
	Input.flush_buffered_events()
	_send_frame.call_deferred()
	return false

func _send_frame() -> void:
	# Godot renders every node, including cameras, UI, shaders and 3D. The
	# private display never appears on the desktop; no node emulation is used.
	var text_state := _collect_text(root)
	# Theme changes queue CanvasItem redraws. Capture only after those deferred
	# redraws have updated the rendering server's commands.
	_capture_frame.call_deferred(text_state)

func _capture_frame(text_state: Dictionary) -> void:
	var render_start := Time.get_ticks_usec()
	RenderingServer.force_draw(false, 1.0 / 60.0)
	var read_start := Time.get_ticks_usec()
	var frame := root.get_texture().get_image()
	var resize_start := Time.get_ticks_usec()
	_restore_text(text_state.restore)
	var render_size := frame.get_size()
	if frame == null or frame.is_empty():
		_send({"error": "Godot did not produce a rendered viewport"})
		quit(2)
		return
	# Character cells are taller than they are wide. Fit the game's aspect
	# ratio into the available terminal space instead of stretching its UI.
	var scale := minf(width * pixel_aspect / frame.get_width(), float(height) / frame.get_height())
	var fitted := Vector2i(maxi(1, roundi(frame.get_width() * scale / pixel_aspect)), maxi(1, roundi(frame.get_height() * scale)))
	fitted.x = mini(fitted.x, width)
	fitted.y = mini(fitted.y, height)
	frame.resize(fitted.x, fitted.y, Image.INTERPOLATE_BILINEAR)
	frame.convert(Image.FORMAT_RGB8)
	var output := Image.create(width, height, false, Image.FORMAT_RGB8)
	output.fill(Color.BLACK)
	output.blit_rect(frame, Rect2i(Vector2i.ZERO, fitted), Vector2i((width - fitted.x) / 2, (height - fitted.y) / 2))
	frame = output
	_send({"width": width, "height": height, "pixels": Marshalls.raw_to_base64(frame.get_data()), "text": _fit_text(text_state.items, render_size, fitted), "render_width": render_size.x, "render_height": render_size.y, "timings": {"render_us": read_start - render_start, "readback_us": resize_start - read_start, "resize_us": Time.get_ticks_usec() - resize_start}})

func _send(value: Dictionary) -> void:
	peer.put_data((JSON.stringify(value) + "\n").to_utf8_buffer())

# Standard axis-aligned UI text becomes semantic terminal text. Custom drawing,
# rich text, rotated controls and subviewports retain Godot's raster rendering.
func _collect_text(node: Node) -> Dictionary:
	var state := {"items": [], "restore": [], "bytes": 0, "order": 0}
	_collect_text_nodes(node, state)
	state.items.sort_custom(func(a: Dictionary, b: Dictionary) -> bool:
		if a.layer != b.layer:
			return a.layer < b.layer
		if a.z != b.z:
			return a.z < b.z
		if a.order != b.order:
			return a.order < b.order
		return a.get("block", false) and not b.get("block", false))
	return state

func _control_rect(node: Control) -> Rect2:
	return (root.get_final_transform() * node.get_global_transform_with_canvas()) * Rect2(Vector2.ZERO, node.size)

func _collect_text_nodes(node: Node, state: Dictionary) -> void:
	state.order += 1
	if node is SubViewport:
		return
	if node is Control and node.is_visible_in_tree() and node.get_viewport() == root:
		var transform: Transform2D = root.get_final_transform() * node.get_global_transform_with_canvas()
		if absf(transform.x.y) < 0.001 and absf(transform.y.x) < 0.001 and transform.x.x > 0 and transform.y.y > 0:
			var rect := _control_rect(node)
			var clip := Rect2(Vector2.ZERO, Vector2(root.size))
			var ancestor := node.get_parent()
			var opacity: float = node.self_modulate.a
			var layer := 0
			var z: int = node.z_index
			var relative: bool = node.z_as_relative
			while ancestor != null:
				if ancestor is CanvasLayer:
					layer = ancestor.layer
				if ancestor is CanvasItem:
					if relative: z += ancestor.z_index
					relative = relative and ancestor.z_as_relative
					opacity *= ancestor.modulate.a
				if ancestor is Control and ancestor.clip_contents:
					clip = clip.intersection(_control_rect(ancestor))
				ancestor = ancestor.get_parent()
			opacity *= node.modulate.a
			if opacity > 0.01 and rect.intersects(clip):
				if _text_occluder(node) and state.items.size() < 512:
					state.items.append({"rect": rect.intersection(clip), "block": true, "layer": layer, "z": z, "order": state.order})
				if (node is Label or node is Button) and not node.text.is_empty() and state.items.size() < 512 and state.bytes < 65536:
					var color_name := "font_color"
					if node is Button:
						if node.disabled:
							color_name = "font_disabled_color"
						elif node.is_pressed():
							color_name = "font_pressed_color"
					var color: Color = node.get_theme_color(color_name)
					if color.a <= 0.01:
						for child in node.get_children():
							_collect_text_nodes(child, state)
						return
					var align: int = node.horizontal_alignment if node is Label else node.alignment
					var vertical: int = node.vertical_alignment if node is Label else VERTICAL_ALIGNMENT_CENTER
					var content: String = node.tr(node.text).substr(0, mini(4096, (65536 - state.bytes) / 4))
					state.bytes += content.to_utf8_buffer().size()
					if node is Label and node.visible_characters >= 0:
						content = content.substr(0, node.visible_characters)
					state.items.append({"rect": rect, "clip": clip, "value": content, "align": align, "vertical": vertical, "color": [color.r8, color.g8, color.b8], "layer": layer, "z": z, "order": state.order})
					for key in ["font_color", "font_shadow_color", "font_outline_color", "font_focus_color", "font_hover_color", "font_pressed_color", "font_hover_pressed_color", "font_disabled_color"]:
						state.restore.append([node, key, node.has_theme_color_override(key), node.get_theme_color(key)])
						node.add_theme_color_override(key, Color.TRANSPARENT)
	for child in node.get_children():
		_collect_text_nodes(child, state)

func _restore_text(changes: Array) -> void:
	for change in changes:
		if change[2]:
			change[0].add_theme_color_override(change[1], change[3])
		else:
			change[0].remove_theme_color_override(change[1])

func _fit_text(items: Array, render_size: Vector2i, fitted: Vector2i) -> Array:
	var result: Array = []
	var ratio := Vector2(fitted) / Vector2(render_size)
	var offset := Vector2(Vector2i((width - fitted.x) / 2, (height - fitted.y) / 2))
	for item in items:
		var rect: Rect2 = item.rect
		item["x"] = rect.position.x * ratio.x + offset.x
		item["y"] = rect.position.y * ratio.y + offset.y
		item["w"] = rect.size.x * ratio.x
		item["h"] = rect.size.y * ratio.y
		item.erase("rect")
		item.erase("layer")
		item.erase("z")
		item.erase("order")
		if item.has("clip"):
			var clip: Rect2 = item.clip
			item["clip"] = [clip.position.x * ratio.x + offset.x, clip.position.y * ratio.y + offset.y, clip.end.x * ratio.x + offset.x, clip.end.y * ratio.y + offset.y]
		result.append(item)
	return result

func _text_occluder(node: Control) -> bool:
	var style: StyleBox
	if node is Panel or node is PanelContainer:
		style = node.get_theme_stylebox("panel")
	elif node is Button:
		style = node.get_theme_stylebox("normal")
	else:
		return false
	if style is StyleBoxFlat:
		return style.bg_color.a > 0.95
	return style is StyleBoxTexture and style.draw_center
