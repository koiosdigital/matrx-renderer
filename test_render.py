#!/usr/bin/env python3
"""
Simple test script to send render requests to the MATRX renderer.
Requires: pip install pika
"""

import json
import pika
import uuid
import time

def send_render_request():
    # Connect to RabbitMQ
    connection = pika.BlockingConnection(
        pika.ConnectionParameters('localhost')
    )
    channel = connection.channel()

    # Declare exchange and input queue
    channel.exchange_declare(exchange='matrx', exchange_type='topic', durable=True)
    channel.queue_declare(queue='matrx.renderer_requests', durable=True)
    
    # Bind input queue
    channel.queue_bind(exchange='matrx', queue='matrx.renderer_requests', routing_key='renderer_requests')

    device_id = f"test-device-{uuid.uuid4()}"
    
    # Create test message
    request = {
        "type": "render_request",
        "app_id": "clock",
        "device": {
            "id": device_id,
            "width": 64,
            "height": 32
        },
        "params": {
            "timezone": "America/New_York",
            "format": "12h"
        }
    }

    # Declare and bind device-specific result queue
    device_queue = f"matrx.{device_id}"
    channel.queue_declare(queue=device_queue, durable=True)
    channel.queue_bind(exchange='matrx', queue=device_queue, routing_key=device_id)

    # Publish message
    channel.basic_publish(
        exchange='matrx',
        routing_key='renderer_requests',
        body=json.dumps(request),
        properties=pika.BasicProperties(
            content_type='application/json',
            delivery_mode=2,  # Make message persistent
            correlation_id=str(uuid.uuid4())
        )
    )

    print(f"Sent render request: {json.dumps(request, indent=2)}")
    print(f"Listening for result on queue: {device_queue}")

    # Set up result consumer
    def callback(ch, method, properties, body):
        result = json.loads(body)
        print(f"Received result: {json.dumps(result, indent=2)}")
        print(f"Render output size: {len(result.get('render_output', ''))} characters (base64)")
        ch.basic_ack(delivery_tag=method.delivery_tag)
        ch.stop_consuming()

    # Consume result from device-specific queue
    channel.basic_consume(queue=device_queue, on_message_callback=callback)
    print("Waiting for result...")
    
    # Wait for result with timeout
    start_time = time.time()
    while time.time() - start_time < 30:  # 30 second timeout
        try:
            connection.process_data_events(time_limit=1)
            if not channel._consumer_infos:
                break
        except:
            break

    connection.close()

if __name__ == "__main__":
    send_render_request()
